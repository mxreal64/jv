// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package lifecycle

import (
	"sync"

	"github.com/mxreal64/jv/pom"
)

// BuildContext is everything an executor needs to know about the project being
// built. It is constructed once per module in buildModule and passed to every
// executor in the lifecycle.
type BuildContext struct {
	// coordinates
	Group, Artifact, Version string
	Packaging                string
	Dir                      string // absolute project directory

	// source layout (defaults: src/main/java, src/test/java, etc.)
	SourceDirs            []string
	TestSourceDirs        []string
	ResourceDirs          []string
	TestResourceDirs      []string
	ResourceFiltering     bool
	TestResourceFiltering bool

	// output layout
	ClassesDir     string // target/classes
	TestClassesDir string // target/test-classes
	GeneratedDir   string // target/generated-sources/annotations
	TargetDir      string // target/

	// resolved dependency classpaths
	CompileCP []string
	TestCP    []string
	RuntimeCP []string

	// effective properties (settings profiles + POM + -D flags)
	Props map[string]string

	// plugin configs keyed by artifactId, for plugin detection
	PluginConfigs map[string]pom.PluginConfig

	// local repository path
	Repo string

	// Source roots added by executors (code generators, plugins). Executors receive the context by
	// value, so roots are also recorded in Roots, which every copy of the context shares: a root added in
	// generate-sources is therefore seen by compile. Use AddSourceRoot rather than appending directly.
	ExtraSourceRoots     []string
	ExtraTestSourceRoots []string
	Roots                *SourceRoots
}

// SourceRoots is the set of generated source directories of one module build, shared by every copy of
// its BuildContext.
type SourceRoots struct {
	mu         sync.Mutex
	main, test []string
}

func (r *SourceRoots) add(dir string, test bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if test {
		r.test = append(r.test, dir)
	} else {
		r.main = append(r.main, dir)
	}
}

func (r *SourceRoots) snapshot(test bool) []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if test {
		return append([]string(nil), r.test...)
	}
	return append([]string(nil), r.main...)
}

// AddSourceRoot registers a generated source directory so that later phases compile it.
func (bc *BuildContext) AddSourceRoot(dir string, test bool) {
	if test {
		bc.ExtraTestSourceRoots = append(bc.ExtraTestSourceRoots, dir)
	} else {
		bc.ExtraSourceRoots = append(bc.ExtraSourceRoots, dir)
	}
	if bc.Roots != nil {
		bc.Roots.add(dir, test)
	}
}

func uniqueDirs(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, d := range l {
			if d != "" && !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// AllSourceDirs returns SourceDirs plus every source root added by executors.
func (bc *BuildContext) AllSourceDirs() []string {
	return uniqueDirs(bc.SourceDirs, bc.ExtraSourceRoots, bc.Roots.snapshot(false))
}

// AllTestSourceDirs returns TestSourceDirs plus every test source root added by executors.
func (bc *BuildContext) AllTestSourceDirs() []string {
	return uniqueDirs(bc.TestSourceDirs, bc.ExtraTestSourceRoots, bc.Roots.snapshot(true))
}
