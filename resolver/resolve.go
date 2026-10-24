// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mxreal64/jv/pom"
)

// Node is one resolved dependency in the (nearest-wins) tree.
type Node struct {
	Dep      pom.Dependency
	Scope    string
	Depth    int
	Children []*Node

	parent   *Node
	rawScope string // scope as declared by the parent's POM, before mediation
	excl     []string
	eff      *Effective
	err      error
}

// Artifact is a resolved, downloaded dependency.
type Artifact struct {
	GroupID, ArtifactID, Version, Type, Classifier, Scope, Path string
}

func trailOf(parent *Node, d pom.Dependency) string {
	parts := []string{d.ArtifactID + ":" + d.Version}
	for x := parent; x != nil; x = x.parent {
		parts = append([]string{x.Dep.ArtifactID + ":" + x.Dep.Version}, parts...)
	}
	return strings.Join(parts, " -> ")
}

func exclKeys(es []pom.Exclusion) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.GroupID + ":" + e.ArtifactID
	}
	return out
}

func excluded(list []string, d pom.Dependency) bool {
	for _, e := range list {
		var g, a string
		for i := 0; i < len(e); i++ {
			if e[i] == ':' {
				g, a = e[:i], e[i+1:]
				break
			}
		}
		if (g == "*" || g == d.GroupID) && (a == "*" || a == d.ArtifactID) {
			return true
		}
	}
	return false
}

func rank(s string) int {
	switch s {
	case "compile":
		return 4
	case "runtime":
		return 3
	case "provided":
		return 2
	}
	return 1
}

// derive implements Maven's transitive scope mediation; "" means the dependency is dropped.
func derive(parent, child string) string {
	if child == "" {
		child = "compile"
	}
	if child != "compile" && child != "runtime" {
		return ""
	}
	if parent == "" || parent == "compile" {
		return child
	}
	return parent
}

// widenChildren re-derives the scope of n's already-resolved descendants after n's own
// scope was widened (Maven derives children from the winner's final scope).
func widenChildren(n *Node) {
	for _, c := range n.Children {
		if ns := derive(n.Scope, c.rawScope); ns != "" && rank(ns) > rank(c.Scope) {
			c.Scope = ns
			widenChildren(c)
		}
	}
}

type cand struct {
	dep    pom.Dependency
	scope  string
	raw    string // declared scope of a transitive dependency (derived against its parent when processed)
	excl   []string
	parent *Node
}

// Resolve walks the dependency graph breadth-first. The nearest declaration of each
// groupId:artifactId:type:classifier wins (first declared on ties); the root's
// dependencyManagement overrides transitive versions.
func (l *Loader) Resolve(root *Effective) ([]*Node, error) {
	var level []cand
	for _, d := range root.Deps {
		level = append(level, cand{dep: d, scope: d.Scope})
	}
	win := map[string]*Node{}
	var top []*Node
	for depth := 1; len(level) > 0; depth++ {
		var winners []*Node
		for _, c := range level {
			if c.parent != nil && excluded(c.excl, c.dep) {
				continue
			}
			scope := c.scope
			if c.parent != nil { // derive from the parent's current (possibly widened) scope
				scope = derive(c.parent.Scope, c.raw)
			}
			if scope == "system" {
				l.warnf("ignoring system-scoped dependency %s:%s", c.dep.GroupID, c.dep.ArtifactID)
				continue
			}
			key := c.dep.Key()
			if w, ok := win[key]; ok {
				if w.Depth > 1 && rank(scope) > rank(w.Scope) {
					w.Scope = scope
					widenChildren(w)
				}
				continue
			}
			if c.dep.Version == "" {
				return nil, fmt.Errorf("%s: no version (declare it or manage it)", trailOf(c.parent, c.dep))
			}
			if isRange(c.dep.Version) {
				v, err := l.resolveRange(c.dep.GroupID, c.dep.ArtifactID, c.dep.Version)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", trailOf(c.parent, c.dep), err)
				}
				c.dep.Version = v
			}
			n := &Node{
				Dep:      c.dep,
				Scope:    scope,
				Depth:    depth,
				parent:   c.parent,
				rawScope: c.raw,
				excl:     append(append([]string(nil), c.excl...), exclKeys(c.dep.Exclusions)...),
			}
			win[key] = n
			if c.parent == nil {
				top = append(top, n)
			} else {
				c.parent.Children = append(c.parent.Children, n)
			}
			winners = append(winners, n)
		}

		var wg sync.WaitGroup
		for _, n := range winners {
			wg.Add(1)
			go func(n *Node) {
				defer wg.Done()
				n.eff, n.err = l.Effective(n.Dep.GroupID, n.Dep.ArtifactID, n.Dep.Version)
			}(n)
		}
		wg.Wait()

		level = nil
		for _, n := range winners {
			if n.err != nil {
				if errors.Is(n.err, ErrNotFound) {
					l.warnf("no POM for %s:%s:%s; assuming no dependencies", n.Dep.GroupID, n.Dep.ArtifactID, n.Dep.Version)
					continue
				}
				return nil, fmt.Errorf("%s: %w", trailOf(n.parent, n.Dep), n.err)
			}
			for _, cd := range n.eff.Deps {
				if cd.Optional == "true" || cd.Scope == "test" || cd.Scope == "provided" || cd.Scope == "system" {
					continue
				}
				if m, ok := root.Mgmt[cd.Key()]; ok {
					if m.Version != "" {
						cd.Version = m.Version
					}
					if m.Scope != "" {
						cd.Scope = m.Scope
					}
					if len(cd.Exclusions) == 0 {
						cd.Exclusions = append([]pom.Exclusion{}, m.Exclusions...)
					}
				}
				if cs := derive(n.Scope, cd.Scope); cs != "" {
					level = append(level, cand{dep: cd, scope: cs, raw: cd.Scope, excl: n.excl, parent: n})
				}
			}
		}
	}
	return top, nil
}

// Flatten returns the winning nodes in depth-first preorder (Maven's classpath order).
func Flatten(roots []*Node) []*Node {
	var out []*Node
	var walk func([]*Node)
	walk = func(ns []*Node) {
		for _, n := range ns {
			out = append(out, n)
			walk(n.Children)
		}
	}
	walk(roots)
	return out
}

func fileName(d pom.Dependency) (name, ext string) {
	ext, cls := d.Type, d.Classifier
	switch ext {
	case "test-jar":
		ext = "jar"
		if cls == "" {
			cls = "tests"
		}
	case "bundle", "ejb", "maven-plugin", "eclipse-plugin":
		ext = "jar"
	}
	name = d.ArtifactID + "-" + d.Version
	if cls != "" {
		name += "-" + cls
	}
	return name + "." + ext, ext
}

// Artifacts downloads every jar in parallel and returns them in tree order.
func (l *Loader) Artifacts(nodes []*Node) ([]Artifact, error) {
	arts := make([]Artifact, len(nodes))
	errs := make([]error, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		d := n.Dep
		arts[i] = Artifact{
			GroupID:    d.GroupID,
			ArtifactID: d.ArtifactID,
			Version:    d.Version,
			Type:       d.Type,
			Classifier: d.Classifier,
			Scope:      n.Scope,
		}
		if mod := l.module(d.GroupID, d.ArtifactID, d.Version); mod != nil {
			if mod.Eff.Packaging == "jar" { // reactor sibling: use its build output directly
				arts[i].Path = mod.Classes
				if d.Type == "test-jar" || d.Classifier == "tests" {
					arts[i].Path = mod.TestClasses
				}
			}
			continue
		}
		name, ext := fileName(d)
		if ext != "jar" {
			continue
		}
		wg.Add(1)
		go func(i int, d pom.Dependency, name string) {
			defer wg.Done()
			arts[i].Path, errs[i] = l.fetchFile(d.GroupID, d.ArtifactID, d.Version, name, true)
		}(i, d, name)
	}
	wg.Wait()
	var out []Artifact
	for i, a := range arts {
		if errs[i] != nil {
			return nil, fmt.Errorf("%s: %w", trailOf(nodes[i].parent, nodes[i].Dep), errs[i])
		}
		if a.Path != "" {
			out = append(out, a)
		}
	}
	return out, nil
}

// Classpath selects jar paths for "compile" (compile+provided), "runtime", or anything else (all scopes).
func Classpath(arts []Artifact, kind string) []string {
	var out []string
	for _, a := range arts {
		ok := true
		switch kind {
		case "compile":
			ok = a.Scope == "compile" || a.Scope == "provided"
		case "runtime":
			ok = a.Scope == "compile" || a.Scope == "runtime"
		}
		if ok {
			out = append(out, a.Path)
		}
	}
	return out
}

func PrintTree(w io.Writer, roots []*Node) {
	var rec func(ns []*Node, prefix string)
	rec = func(ns []*Node, prefix string) {
		for i, n := range ns {
			branch, next := "├─ ", "│  "
			if i == len(ns)-1 {
				branch, next = "└─ ", "   "
			}
			fmt.Fprintf(w, "%s%s%s:%s:%s (%s)\n", prefix, branch, n.Dep.GroupID, n.Dep.ArtifactID, n.Dep.Version, n.Scope)
			rec(n.Children, prefix+next)
		}
	}
	rec(roots, "")
}

type lockFile struct {
	Key       string     `json:"key"`
	Artifacts []Artifact `json:"artifacts"`
}

// ReadLock returns the cached resolution if the key matches and every jar still exists.
func ReadLock(file, key string) ([]Artifact, bool) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, false
	}
	var lf lockFile
	if json.Unmarshal(data, &lf) != nil || lf.Key != key {
		return nil, false
	}
	for _, a := range lf.Artifacts {
		if _, err := os.Stat(a.Path); err != nil {
			return nil, false
		}
	}
	return lf.Artifacts, true
}

// WriteLock writes the lock file atomically using a temporary file.
func WriteLock(file, key string, arts []Artifact) error {
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(lockFile{key, arts}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".lock-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), file)
}
