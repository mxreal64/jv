// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package pom

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/jv-build/jv/jdk"
)

// Env describes the context profile activation is evaluated in.
type Env struct {
	IDs     map[string]bool   // explicit -P ids ("!id" deactivates)
	Props   map[string]string // user properties plus env.*
	JDK     string            // running JDK version such as "17.0.2"; "" if unknown
	JDKFunc func() string     // lazily computes JDK when it is empty
	OS      string            // runtime.GOOS; "" if unknown
	Arch    string            // runtime.GOARCH
	Dir     string            // project directory, for <file> activation
}

// NewEnv builds the real environment: the JDK is detected lazily (only when a profile
// actually has a <jdk> condition), the OS comes from the Go runtime.
func NewEnv(ids map[string]bool, props map[string]string, dir string) Env {
	all := make(map[string]string, len(props)+32)
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			all["env."+kv[:i]] = kv[i+1:]
		}
	}
	for k, v := range props {
		all[k] = v
	}
	return Env{IDs: ids, Props: all, JDKFunc: jdk.Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Dir: dir}
}

func (e Env) jdk() string {
	if e.JDK != "" {
		return e.JDK
	}
	if e.JDKFunc != nil {
		return e.JDKFunc()
	}
	return ""
}

func (a Activation) matchesProperty(props map[string]string) bool {
	if a.Property == nil {
		return false
	}
	name, want := strings.TrimSpace(a.Property.Name), strings.TrimSpace(a.Property.Value)
	neg := strings.HasPrefix(name, "!")
	name = strings.TrimPrefix(name, "!")
	if name == "" {
		return false
	}
	got, ok := props[name]
	if neg {
		return !ok
	}
	switch {
	case want == "":
		return ok
	case strings.HasPrefix(want, "!"):
		return ok && got != want[1:]
	}
	return ok && got == want
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func dottedTokens(v string) []int {
	parts := strings.FieldsFunc(v, func(r rune) bool { return r < '0' || r > '9' })
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

// cmpDotted compares numeric dotted versions. With trunc, a is cut to as many components
// as b has, so the bound "1.8" accepts "1.8.0_292".
func cmpDotted(a, b string, trunc bool) int {
	ta, tb := dottedTokens(a), dottedTokens(b)
	if trunc && len(ta) > len(tb) {
		ta = ta[:len(tb)]
	}
	for i := 0; i < len(ta) || i < len(tb); i++ {
		x, y := 0, 0
		if i < len(ta) {
			x = ta[i]
		}
		if i < len(tb) {
			y = tb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionPrefix(ver, spec string) bool {
	if !strings.HasPrefix(ver, spec) {
		return false
	}
	return len(ver) == len(spec) || !isDigit(ver[len(spec)])
}

func inRange(spec, ver string) bool {
	end := strings.IndexAny(spec, "])")
	if end < 0 {
		return false
	}
	loInc, hiInc := spec[0] == '[', spec[end] == ']'
	parts := strings.Split(spec[1:end], ",")
	if len(parts) == 1 {
		return versionPrefix(ver, strings.TrimSpace(parts[0]))
	}
	if lo := strings.TrimSpace(parts[0]); lo != "" {
		if c := cmpDotted(ver, lo, false); c < 0 || (c == 0 && !loInc) {
			return false
		}
	}
	if hi := strings.TrimSpace(parts[1]); hi != "" {
		if c := cmpDotted(ver, hi, true); c > 0 || (c == 0 && !hiInc) {
			return false
		}
	}
	return true
}

// JDKMatches evaluates a <jdk> activation spec ("17", "!1.8", "[9,)") against a version.
func JDKMatches(spec, ver string) bool {
	spec = strings.TrimSpace(spec)
	neg := strings.HasPrefix(spec, "!")
	spec = strings.TrimPrefix(spec, "!")
	var ok bool
	if strings.HasPrefix(spec, "[") || strings.HasPrefix(spec, "(") {
		ok = inRange(spec, ver)
	} else {
		ok = versionPrefix(ver, spec)
	}
	return ok != neg
}

func negated(spec string, f func(string) bool) bool {
	neg := strings.HasPrefix(spec, "!")
	return f(strings.ToLower(strings.TrimPrefix(spec, "!"))) != neg
}

func (o *OSActivation) matches(goos, goarch string) bool {
	if n := strings.TrimSpace(o.Name); n != "" {
		ok := negated(n, func(s string) bool {
			switch goos {
			case "darwin":
				return s == "mac os x"
			case "windows":
				return strings.HasPrefix(s, "windows")
			}
			return s == goos
		})
		if !ok {
			return false
		}
	}
	if f := strings.TrimSpace(o.Family); f != "" {
		ok := negated(f, func(s string) bool {
			switch s {
			case "windows":
				return goos == "windows"
			case "mac":
				return goos == "darwin"
			case "unix":
				return goos != "windows"
			}
			return s == goos
		})
		if !ok {
			return false
		}
	}
	if a := strings.TrimSpace(o.Arch); a != "" {
		ok := negated(a, func(s string) bool {
			switch goarch {
			case "amd64":
				return s == "amd64" || s == "x86_64"
			case "arm64":
				return s == "aarch64" || s == "arm64"
			case "386":
				return s == "x86" || s == "i386"
			}
			return s == goarch
		})
		if !ok {
			return false
		}
	}
	return true
}

func (f *FileActivation) matches(dir string) bool {
	resolve := func(p string) string {
		p = strings.TrimSpace(p)
		p = strings.ReplaceAll(p, "${basedir}", dir)
		p = strings.ReplaceAll(p, "${project.basedir}", dir)
		if home, err := os.UserHomeDir(); err == nil {
			p = strings.ReplaceAll(p, "${user.home}", home)
		}
		if !filepath.IsAbs(p) && dir != "" {
			p = filepath.Join(dir, p)
		}
		return p
	}
	if f.Exists != "" {
		if _, err := os.Stat(resolve(f.Exists)); err != nil {
			return false
		}
	}
	if f.Missing != "" {
		if _, err := os.Stat(resolve(f.Missing)); err == nil {
			return false
		}
	}
	return true
}

// matches evaluates every declared condition (all must hold, as in Maven 3.2.2+).
// unsupported is true when a jdk/os condition cannot be evaluated because the
// environment is unknown.
func (a Activation) matches(e Env) (ok, unsupported bool) {
	has := false
	if a.Property != nil {
		has = true
		if !a.matchesProperty(e.Props) {
			return false, false
		}
	}
	if a.File != nil {
		has = true
		if !a.File.matches(e.Dir) {
			return false, false
		}
	}
	if j := strings.TrimSpace(a.JDK); j != "" {
		has = true
		ver := e.jdk()
		if ver == "" {
			return false, true
		}
		if !JDKMatches(j, ver) {
			return false, false
		}
	}
	if a.OS != nil {
		has = true
		if e.OS == "" {
			return false, true
		}
		if !a.OS.matches(e.OS, e.Arch) {
			return false, false
		}
	}
	return has, false
}

// ApplyProfiles is ApplyProfilesEnv without JDK/OS knowledge: profiles with jdk/os
// conditions are reported as unsupported unless selected explicitly.
func (m *Model) ApplyProfiles(ids map[string]bool, props map[string]string) (unsupported []string) {
	return m.ApplyProfilesEnv(Env{IDs: ids, Props: props})
}

// ApplyProfilesEnv activates profiles and overlays them onto the model. A profile is active
// when its id is in e.IDs ("!id" disables it) or all of its activation conditions (property,
// file, jdk, os) match; activeByDefault profiles apply only if no other profile in this POM
// is active. It returns ids of profiles whose jdk/os condition could not be evaluated.
func (m *Model) ApplyProfilesEnv(e Env) (unsupported []string) {
	var active, defaults []Profile
	for _, p := range m.Profiles {
		id := strings.TrimSpace(p.ID)
		if e.IDs["!"+id] {
			continue
		}
		explicit := e.IDs[id]
		matched, unsup := p.Activation.matches(e)
		if unsup && !explicit {
			unsupported = append(unsupported, id)
		}
		switch {
		case explicit || matched:
			active = append(active, p)
		case strings.TrimSpace(p.Activation.ActiveByDefault) == "true":
			defaults = append(defaults, p)
		}
	}
	if len(active) == 0 {
		active = defaults
	}
	for _, p := range active {
		m.Properties.Items = append(m.Properties.Items, p.Properties.Items...)
		m.Modules = append(m.Modules, p.Modules...)
		m.Dependencies = overlayDeps(m.Dependencies, p.Dependencies)
		m.DependencyManagement.Dependencies = overlayDeps(m.DependencyManagement.Dependencies, p.DependencyManagement.Dependencies)
		m.Repositories = append(m.Repositories, p.Repositories...)
	}
	return unsupported
}
