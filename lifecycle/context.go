// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package lifecycle

import "github.com/mxreal64/jv/pom"

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

	// mutable: executors may append source roots (e.g. code generators)
	ExtraSourceRoots     []string
	ExtraTestSourceRoots []string
}

// AllSourceDirs returns SourceDirs plus any ExtraSourceRoots added by prior executors.
func (bc *BuildContext) AllSourceDirs() []string {
	return append(append([]string{}, bc.SourceDirs...), bc.ExtraSourceRoots...)
}

// AllTestSourceDirs returns TestSourceDirs plus any ExtraTestSourceRoots.
func (bc *BuildContext) AllTestSourceDirs() []string {
	return append(append([]string{}, bc.TestSourceDirs...), bc.ExtraTestSourceRoots...)
}
