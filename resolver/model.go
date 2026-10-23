// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package resolver

import (
	"crypto/sha256"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mxreal64/jv/pom"
)

// Effective is a POM after inheritance, interpolation, dependencyManagement and BOM imports.
type Effective struct {
	G, A, V   string
	Packaging string
	Props     map[string]string
	Deps      []pom.Dependency
	Mgmt      map[string]pom.Dependency
	Build     pom.Build

	// Unsupported lists <build> settings (own or inherited) that jv ignores.
	Unsupported []string
}

// raw is the merged (still uninterpolated) parent chain.
type raw struct {
	g, a, v    string
	pg, pa, pv string
	packaging  string
	basedir    string
	props      map[string]string
	mgmt       []pom.Dependency
	deps       []pom.Dependency
	build      pom.Build

	unsupported []string
}

// mergeDeps follows Maven's inheritance: the child's entries first, then the
// parent's entries that the child does not override.
func mergeDeps(parent, child []pom.Dependency) []pom.Dependency {
	if len(child) == 0 {
		return parent // share the parent list; callers treat it as read-only
	}
	out := append([]pom.Dependency{}, child...)
	seen := map[string]bool{}
	for _, d := range child {
		seen[d.Key()] = true
	}
	for _, d := range parent {
		if !seen[d.Key()] {
			out = append(out, d)
		}
	}
	return out
}

// mergePluginManagement merges parent pluginManagement into child: child entries win per artifactId.
func mergePluginManagement(parent, child []pom.Plugin) []pom.Plugin {
	if len(parent) == 0 {
		return child
	}
	if len(child) == 0 {
		return parent
	}
	childIdx := map[string]bool{}
	for _, p := range child {
		childIdx[strings.TrimSpace(p.ArtifactID)] = true
	}
	out := append([]pom.Plugin{}, child...)
	for _, p := range parent {
		if !childIdx[strings.TrimSpace(p.ArtifactID)] {
			out = append(out, p)
		}
	}
	return out
}

func mergeRaw(m *pom.Model, parent *raw) *raw {
	r := &raw{g: m.GroupID, a: m.ArtifactID, v: m.Version, packaging: m.Packaging, props: map[string]string{}}
	if m.Parent != nil {
		r.pg, r.pa, r.pv = m.Parent.GroupID, m.Parent.ArtifactID, m.Parent.Version
	}
	var pm, pd []pom.Dependency
	if parent != nil {
		for k, v := range parent.props {
			r.props[k] = v
		}
		pm, pd = parent.mgmt, parent.deps
	}
	for k, v := range m.PropMap() {
		r.props[k] = v
	}
	for k, v := range m.Build.CompilerProps() { // explicit plugin configuration wins over properties
		r.props[k] = v
	}
	r.build = m.Build
	if parent != nil {
		r.build.PluginManagement = mergePluginManagement(parent.build.PluginManagement, m.Build.PluginManagement)
	}
	if parent != nil {
		r.unsupported = append(r.unsupported, parent.unsupported...)
	}
	r.unsupported = append(r.unsupported, m.Build.Unsupported()...)
	r.mgmt = mergeDeps(pm, m.DependencyManagement.Dependencies)
	r.deps = mergeDeps(pd, m.Dependencies)
	return r
}

func (l *Loader) rawFor(g, a, v string, chain []string) (*raw, error) {
	k := g + ":" + a + ":" + v
	for _, c := range chain {
		if c == k {
			return nil, fmt.Errorf("cyclic parent chain at %s", k)
		}
	}
	l.mu.Lock()
	e := l.raws[k]
	if e == nil {
		e = &rawEntry{}
		l.raws[k] = e
	}
	l.mu.Unlock()
	e.once.Do(func() {
		next := append(append([]string(nil), chain...), k)
		e.r, e.err = l.loadRaw(g, a, v, next)
	})
	return e.r, e.err
}

func (l *Loader) loadRaw(g, a, v string, chain []string) (*raw, error) {
	file, err := l.fetchFile(g, a, v, a+"-"+v+".pom", true)
	if err != nil {
		return nil, err
	}
	m, err := pom.ParsePOM(file)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", file, err)
	}
	m.ApplyProfilesEnv(pom.NewEnv(l.ProfileIDs, l.UserProps, ""))
	l.addRepos(m.Repositories, false) // declared by a dependency's POM: untrusted
	var parent *raw
	if m.Parent != nil {
		if parent, err = l.rawFor(m.Parent.GroupID, m.Parent.ArtifactID, m.Parent.Version, chain); err != nil {
			return nil, fmt.Errorf("parent of %s:%s:%s: %w", g, a, v, err)
		}
	}
	return mergeRaw(m, parent), nil
}

// Effective returns the effective model of a repository artifact (memoized).
func (l *Loader) Effective(g, a, v string) (*Effective, error) { return l.effective(g, a, v, nil) }

func (l *Loader) effective(g, a, v string, chain []string) (*Effective, error) {
	if mod := l.module(g, a, v); mod != nil {
		return mod.Eff, nil
	}
	k := g + ":" + a + ":" + v
	for _, c := range chain {
		if c == k {
			return nil, fmt.Errorf("cyclic BOM import at %s", k)
		}
	}
	l.mu.Lock()
	e := l.effs[k]
	if e == nil {
		e = &effEntry{}
		l.effs[k] = e
	}
	l.mu.Unlock()
	e.once.Do(func() {
		r, err := l.rawFor(g, a, v, nil)
		if err != nil {
			e.err = err
			return
		}
		e.e, e.err = l.finalize(r, append(append([]string(nil), chain...), k))
	})
	return e.e, e.err
}

// LoadRoot builds the effective model of a local pom.xml (following local relativePath
// parents when they match). Everything read from disk is written to h for lock keys.
func (l *Loader) LoadRoot(file string, h hash.Hash) (*Effective, error) {
	r, err := l.rootRaw(file, h, 0)
	if err != nil {
		return nil, err
	}
	h.Write([]byte("\nopts\n" + l.optionsKey())) // -P/-D change resolution but are not in any POM
	r.basedir = filepath.Dir(file)
	return l.finalize(r, []string{"root"})
}

func (l *Loader) rootRaw(file string, h hash.Hash, depth int) (*raw, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	h.Write([]byte(file + "\n"))
	h.Write(data)
	m, err := pom.ParsePOMBytes(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", file, err)
	}
	for _, id := range m.ApplyProfilesEnv(pom.NewEnv(l.ProfileIDs, l.UserProps, filepath.Dir(file))) {
		l.warnf("%s: cannot evaluate the jdk/os activation of profile %q (enable it explicitly with -P %s)", file, id, id)
	}
	l.addRepos(m.Repositories, true) // the project's own POMs: trusted
	var parent *raw
	if p := m.Parent; p != nil {
		local := false
		if depth < 20 && (p.RelativePath == nil || *p.RelativePath != "") {
			rel := "../pom.xml"
			if p.RelativePath != nil {
				rel = *p.RelativePath
			}
			pf := filepath.Join(filepath.Dir(file), rel)
			if st, e := os.Stat(pf); e == nil && st.IsDir() {
				pf = filepath.Join(pf, "pom.xml")
			}
			if pm, e := pom.ParsePOM(pf); e == nil && pm.ArtifactID == p.ArtifactID && pm.GroupID == p.GroupID {
				if parent, err = l.rootRaw(pf, h, depth+1); err != nil {
					return nil, err
				}
				local = true
			}
		}
		if !local {
			if parent, err = l.rawFor(p.GroupID, p.ArtifactID, p.Version, nil); err != nil {
				return nil, fmt.Errorf("parent %s:%s:%s: %w", p.GroupID, p.ArtifactID, p.Version, err)
			}
		}
	}
	return mergeRaw(m, parent), nil
}

func interpDep(d pom.Dependency, props map[string]string) pom.Dependency {
	f := func(s string) string { return strings.TrimSpace(pom.Interpolate(s, props)) }
	d.GroupID, d.ArtifactID, d.Version = f(d.GroupID), f(d.ArtifactID), f(d.Version)
	d.Type, d.Classifier, d.Scope, d.Optional = f(d.Type), f(d.Classifier), f(d.Scope), f(d.Optional)
	ex := make([]pom.Exclusion, len(d.Exclusions))
	for i, e := range d.Exclusions {
		ex[i] = pom.Exclusion{GroupID: f(e.GroupID), ArtifactID: f(e.ArtifactID)}
	}
	d.Exclusions = ex
	return d
}

func unionExcl(a, b []pom.Exclusion) []pom.Exclusion {
	out := append([]pom.Exclusion{}, a...)
	for _, e := range b {
		dup := false
		for _, x := range out {
			if x == e {
				dup = true
			}
		}
		if !dup {
			out = append(out, e)
		}
	}
	return out
}

func (l *Loader) finalize(r *raw, chain []string) (*Effective, error) {
	// Start from the env snapshot taken once at Loader construction (avoids
	// calling os.Environ on every POM in a large dependency graph).
	props := make(map[string]string, len(l.envCache)+len(r.props)+20)
	for k, v := range l.envCache {
		props[k] = v
	}
	for k, v := range r.props {
		props[k] = v
	}
	for k, v := range l.UserProps {
		props[k] = v
	}
	if r.basedir != "" {
		props["basedir"], props["project.basedir"] = r.basedir, r.basedir
		props["project.build.directory"] = filepath.Join(r.basedir, "target")
		props["project.build.outputDirectory"] = filepath.Join(r.basedir, "target", "classes")
	}
	for _, p := range []string{"project", "pom"} {
		props[p+".groupId"], props[p+".artifactId"], props[p+".version"] = r.g, r.a, r.v
		props[p+".packaging"] = r.packaging
		props[p+".parent.groupId"], props[p+".parent.artifactId"], props[p+".parent.version"] = r.pg, r.pa, r.pv
	}
	props["parent.version"], props["parent.groupId"] = r.pv, r.pg
	for k, v := range map[string]string{"version": r.v, "groupId": r.g, "artifactId": r.a} {
		if _, ok := props[k]; !ok {
			props[k] = v
		}
	}

	for k, v := range props { // resolve ${...} inside property values (e.g. release = ${java.version})
		if strings.Contains(v, "${") {
			props[k] = pom.Interpolate(v, props)
		}
	}
	for k, v := range r.build.CompilerProps() {
		if props[k] == "" {
			props[k] = pom.Interpolate(v, props)
		}
	}

	eff := &Effective{G: r.g, A: r.a, V: r.v, Packaging: r.packaging, Props: props, Mgmt: map[string]pom.Dependency{}, Build: r.build, Unsupported: uniq(r.unsupported)}
	var imports []pom.Dependency
	for _, d := range r.mgmt {
		d = interpDep(d, props)
		if d.Scope == "import" && (d.Type == "pom") {
			imports = append(imports, d)
			continue
		}
		eff.Mgmt[d.Key()] = d
	}
	for _, d := range imports { // explicit entries win; among imports, first wins
		ie, err := l.effective(d.GroupID, d.ArtifactID, d.Version, chain)
		if err != nil {
			return nil, fmt.Errorf("importing BOM %s:%s:%s: %w", d.GroupID, d.ArtifactID, d.Version, err)
		}
		for k, md := range ie.Mgmt {
			if _, ok := eff.Mgmt[k]; !ok {
				eff.Mgmt[k] = md
			}
		}
	}
	for _, d := range r.deps {
		d = interpDep(d, props)
		if m, ok := eff.Mgmt[d.Key()]; ok {
			if d.Version == "" {
				d.Version = pom.Interpolate(m.Version, props)
			}
			if d.Scope == "" {
				d.Scope = m.Scope
			}
			if d.Optional == "" {
				d.Optional = m.Optional
			}
			d.Exclusions = unionExcl(d.Exclusions, m.Exclusions)
		}
		if d.Scope == "" {
			d.Scope = "compile"
		}
		if d.Type == "" {
			d.Type = "jar"
		}
		eff.Deps = append(eff.Deps, d)
	}
	return eff, nil
}

// HashKey finalizes a lock key from a hash that LoadRoot wrote into.
func HashKey(h hash.Hash) string { return fmt.Sprintf("%x", h.Sum([]byte("jv2"))) }

// NewHash returns the hash used for lock keys.
func NewHash() hash.Hash { return sha256.New() }

// optionsKey serializes the command-line inputs (besides POM files) that change resolution.
func (l *Loader) optionsKey() string {
	var parts []string
	for id, on := range l.ProfileIDs {
		if on {
			parts = append(parts, "P"+id)
		}
	}
	for k, v := range l.UserProps {
		parts = append(parts, "D"+k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
