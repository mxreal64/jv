package resolver

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jv-build/jv/pom"
)

// Module is one project in the reactor (a single project is a reactor of one).
type Module struct {
	Dir         string // absolute
	Eff         *Effective
	Key         string // lock key: own POM chain + the whole reactor's POMs
	Classes     string
	TestClasses string
}

func (m *Module) GAV() string { return m.Eff.G + ":" + m.Eff.A + ":" + m.Eff.V }

// ListModuleDirs returns root and, recursively, every <modules> directory (absolute paths),
// including modules declared by active profiles (ids = -P, props = -D).
func ListModuleDirs(root string, ids map[string]bool, props map[string]string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	var walk func(dir string) error
	walk = func(dir string) error {
		dir = filepath.Clean(dir)
		if seen[dir] {
			return nil
		}
		seen[dir] = true
		m, err := pom.ParsePOM(filepath.Join(dir, "pom.xml"))
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		m.ApplyProfilesEnv(pom.NewEnv(ids, props, dir))
		out = append(out, dir)
		for _, mod := range m.Modules {
			mod = strings.TrimSpace(mod)
			p := filepath.Join(dir, filepath.FromSlash(mod))
			if strings.HasSuffix(mod, ".xml") {
				p = filepath.Dir(p)
			}
			if err := walk(p); err != nil {
				return err
			}
		}
		return nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return out, walk(abs)
}

// Discover loads the effective model of every module. Modules that import a sibling BOM
// are retried once their siblings are registered.
func (l *Loader) Discover(root string) ([]*Module, error) {
	dirs, err := ListModuleDirs(root, l.ProfileIDs, l.UserProps)
	if err != nil {
		return nil, err
	}
	mods := make([]*Module, len(dirs))
	keys := make([]string, len(dirs))
	var firstErr error
	for pending := len(dirs); pending > 0; {
		progress := 0
		for i, d := range dirs {
			if mods[i] != nil {
				continue
			}
			h := NewHash()
			eff, err := l.LoadRoot(filepath.Join(d, "pom.xml"), h)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			mods[i] = &Module{
				Dir:         d,
				Eff:         eff,
				Classes:     filepath.Join(d, "target", "classes"),
				TestClasses: filepath.Join(d, "target", "test-classes"),
			}
			keys[i] = HashKey(h)
			l.Register([]*Module{mods[i]})
			progress++
			pending--
		}
		if progress == 0 {
			return nil, firstErr
		}
		if pending > 0 {
			l.resetCaches() // earlier failures may have been cached
			firstErr = nil
		}
	}
	rh := sha256.New()
	for _, k := range keys {
		rh.Write([]byte(k))
	}
	for i, m := range mods {
		m.Key = fmt.Sprintf("%s|%x", keys[i], rh.Sum(nil))
	}
	return mods, nil
}

func siblingOf(byGAV map[string]*Module, d pom.Dependency) *Module {
	return byGAV[d.GroupID+":"+d.ArtifactID+":"+d.Version]
}

// Order sorts modules so every module comes after the siblings it depends on.
func Order(mods []*Module) ([]*Module, error) {
	byGAV := map[string]*Module{}
	for _, m := range mods {
		byGAV[m.GAV()] = m
	}
	state := map[*Module]int{}
	var out []*Module
	var visit func(m *Module, stack []string) error
	visit = func(m *Module, stack []string) error {
		switch state[m] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("dependency cycle between modules: %s -> %s", strings.Join(stack, " -> "), m.Eff.A)
		}
		state[m] = 1
		for _, d := range m.Eff.Deps {
			dm := siblingOf(byGAV, d)
			if dm == nil {
				continue
			}
			if dm == m {
				return fmt.Errorf("module %s depends on itself (%s:%s:%s)", m.Eff.A, d.GroupID, d.ArtifactID, d.Version)
			}
			if err := visit(dm, append(stack, m.Eff.A)); err != nil {
				return err
			}
		}
		state[m] = 2
		out = append(out, m)
		return nil
	}
	for _, m := range mods {
		if err := visit(m, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Select keeps the named modules (artifactId, group:artifactId, or path relative to root)
// plus everything they depend on, preserving build order.
func Select(ordered []*Module, root string, names []string) ([]*Module, error) {
	byGAV := map[string]*Module{}
	for _, m := range ordered {
		byGAV[m.GAV()] = m
	}
	want := map[*Module]bool{}
	var add func(m *Module)
	add = func(m *Module) {
		if want[m] {
			return
		}
		want[m] = true
		for _, d := range m.Eff.Deps {
			if dm := siblingOf(byGAV, d); dm != nil {
				add(dm)
			}
		}
	}
	for _, n := range names {
		found := false
		for _, m := range ordered {
			if moduleMatches(m, root, n) {
				add(m)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("-pl: no module matches %q", n)
		}
	}
	var out []*Module
	for _, m := range ordered {
		if want[m] {
			out = append(out, m)
		}
	}
	return out, nil
}

func moduleMatches(m *Module, root, name string) bool {
	rel, _ := filepath.Rel(root, m.Dir)
	return name == m.Eff.A || name == m.Eff.G+":"+m.Eff.A || name == filepath.ToSlash(rel)
}

// SelectOnly keeps just the named modules (no upstream modules), for goals like
// classpath and tree that report on a module rather than build it.
func SelectOnly(ordered []*Module, root string, names []string) ([]*Module, error) {
	var out []*Module
	for _, m := range ordered {
		for _, n := range names {
			if moduleMatches(m, root, n) {
				out = append(out, m)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("-pl: no module matches %q", strings.Join(names, ","))
	}
	return out, nil
}
