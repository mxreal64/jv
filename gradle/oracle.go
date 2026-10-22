// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

// Package gradle gets a build's model from a real Gradle instead of re-implementing the
// Groovy/Kotlin DSL: an init script dumps every Java project's source dirs, resolved
// classpaths and compiler/test settings as JSON. jv caches that model (keyed by the build
// scripts) and then compiles, tests and packages natively. Anything the model reports that
// jv cannot reproduce makes the caller fall back to running Gradle itself (package bridge).
package gradle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const modelVersion = "1"

// Project is one Gradle project as reported by the init script.
type Project struct {
	Path     string   `json:"path"`
	Dir      string   `json:"dir"`
	Name     string   `json:"name"`
	Group    string   `json:"group"`
	Version  string   `json:"version"`
	BuildDir string   `json:"buildDir"`
	Plugins  []string `json:"plugins"`
	Java     bool     `json:"java"`

	MainJava    []string `json:"mainJava"`
	MainRes     []string `json:"mainRes"`
	TestJava    []string `json:"testJava"`
	TestRes     []string `json:"testRes"`
	MainClasses []string `json:"mainClasses"`
	TestClasses []string `json:"testClasses"`
	MainResOut  string   `json:"mainResOut"`
	TestResOut  string   `json:"testResOut"`

	Compile     []string `json:"compile"`
	Runtime     []string `json:"runtime"`
	TestCompile []string `json:"testCompile"`
	TestRuntime []string `json:"testRuntime"`
	Processors  []string `json:"processors"`

	CompilerArgs []string `json:"compilerArgs"`
	Encoding     string   `json:"encoding"`
	Release      string   `json:"release"`
	Source       string   `json:"source"`
	Target       string   `json:"target"`
	CompileDeps  []string `json:"compileDeps"`

	TestEnabled   bool              `json:"testEnabled"`
	TestFramework string            `json:"testFramework"`
	JvmArgs       []string          `json:"jvmArgs"`
	SystemProps   map[string]string `json:"systemProps"`

	Jar      string            `json:"jar"`
	Manifest map[string]string `json:"manifest"`
}

// Model is the whole build.
type Model struct {
	Root     string    `json:"root"`
	Projects []Project `json:"projects"`
}

// initScript registers a root task, jvModel, that prints the model to -Djv.model.out.
// Everything optional is wrapped in try/catch so older Gradle versions still answer.
const initScript = `
import groovy.json.JsonOutput

def paths = { c -> c == null ? [] : c.collect { it.absolutePath.toString() } }

gradle.rootProject { rp ->
    rp.tasks.register('jvModel') {
        doLast {
            def list = []
            rp.allprojects.each { p ->
                def info = [path: p.path, dir: p.projectDir.absolutePath, name: p.name,
                            group: String.valueOf(p.group), version: String.valueOf(p.version),
                            buildDir: p.layout.buildDirectory.get().asFile.absolutePath,
                            plugins: p.plugins.collect { it.getClass().name }.sort(), java: false]
                if (p.plugins.hasPlugin('java')) {
                    info.java = true
                    def ss = p.extensions.getByName('sourceSets')
                    def m = ss.getByName('main')
                    def t = ss.getByName('test')
                    info.mainJava = paths(m.java.srcDirs)
                    info.mainRes = paths(m.resources.srcDirs)
                    info.testJava = paths(t.java.srcDirs)
                    info.testRes = paths(t.resources.srcDirs)
                    info.mainClasses = paths(m.output.classesDirs.files)
                    info.testClasses = paths(t.output.classesDirs.files)
                    info.mainResOut = m.output.resourcesDir == null ? '' : m.output.resourcesDir.absolutePath
                    info.testResOut = t.output.resourcesDir == null ? '' : t.output.resourcesDir.absolutePath
                    def files = { n ->
                        def c = p.configurations.findByName(n)
                        c == null ? [] : paths(c.files)
                    }
                    info.compile = files('compileClasspath')
                    info.runtime = files('runtimeClasspath')
                    info.testCompile = files('testCompileClasspath')
                    info.testRuntime = files('testRuntimeClasspath')
                    info.processors = files('annotationProcessor') + files('testAnnotationProcessor')
                    def jc = p.tasks.findByName('compileJava')
                    if (jc != null) {
                        info.compilerArgs = jc.options.compilerArgs.collect { it.toString() }
                        info.encoding = jc.options.encoding
                        info.source = jc.sourceCompatibility
                        info.target = jc.targetCompatibility
                        try { info.release = jc.options.release.orNull == null ? '' : jc.options.release.orNull.toString() } catch (ignored) { }
                        try { info.compileDeps = jc.taskDependencies.getDependencies(jc).collect { it.name } } catch (ignored) { }
                    }
                    def tt = p.tasks.findByName('test')
                    if (tt != null) {
                        info.testEnabled = tt.enabled
                        try { info.testFramework = tt.options.getClass().name } catch (ignored) { }
                        info.jvmArgs = (tt.jvmArgs ?: []).collect { it.toString() }
                        info.systemProps = tt.systemProperties.collectEntries { k, v -> [(k.toString()): String.valueOf(v)] }
                    }
                    def jt = p.tasks.findByName('jar')
                    if (jt != null) {
                        info.jar = jt.archiveFile.get().asFile.absolutePath
                        info.manifest = jt.manifest.attributes.collectEntries { k, v -> [(k.toString()): String.valueOf(v)] }
                    }
                }
                list << info
            }
            new File(System.getProperty('jv.model.out')).text = JsonOutput.toJson([root: rp.projectDir.absolutePath, projects: list])
        }
    }
}
`

// Query runs gradleCmd (a gradlew path or "gradle") in root with the init script and returns
// the model. extra are -P/-D arguments that influence the build.
func Query(ctx context.Context, gradleCmd, root string, extra []string, offline bool) (*Model, error) {
	dir, err := os.MkdirTemp("", "jv-gradle-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "jv-model.init.gradle")
	out := filepath.Join(dir, "model.json")
	if err := os.WriteFile(script, []byte(initScript), 0o644); err != nil {
		return nil, err
	}
	args := []string{"-q", "--console=plain", "--no-configuration-cache", "--init-script", script, "-Djv.model.out=" + out}
	if offline {
		args = append(args, "--offline")
	}
	args = append(args, extra...)
	args = append(args, "jvModel")
	cmd := exec.CommandContext(ctx, gradleCmd, args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 600 {
			msg = msg[len(msg)-600:]
		}
		return nil, fmt.Errorf("%s failed: %v: %s", filepath.Base(gradleCmd), err, msg)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("gradle produced no model: %w", err)
	}
	var m Model
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unreadable model: %w", err)
	}
	return &m, nil
}

var skipDirs = map[string]bool{"build": true, ".gradle": true, ".git": true, "node_modules": true, "target": true, ".jv": true, ".idea": true, "out": true}

func isBuildInput(name string) bool {
	switch name {
	case "gradle.properties", "libs.versions.toml", "gradle-wrapper.properties":
		return true
	}
	return strings.HasSuffix(name, ".gradle") || strings.HasSuffix(name, ".gradle.kts")
}

// CacheKey hashes every file that can change the model: build scripts, settings, version
// catalogs, gradle.properties, the wrapper version, and the -P/-D arguments.
func CacheKey(root string, extra []string) (string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if isBuildInput(d.Name()) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".gradle", "gradle.properties"))
	}
	sort.Strings(files)
	h := sha256.New()
	fmt.Fprintf(h, "jv-gradle-model-%s\n%s\n", modelVersion, strings.Join(extra, "\x00"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue // optional files such as ~/.gradle/gradle.properties
		}
		rel, rerr := filepath.Rel(root, f)
		if rerr != nil {
			rel = f
		}
		fmt.Fprintf(h, "%s\n%d\n", filepath.ToSlash(rel), len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type cacheFile struct {
	Key   string `json:"key"`
	Model *Model `json:"model"`
}

func cachePath(root string) string { return filepath.Join(root, ".jv", "gradle-model.json") }

// LoadCache returns the cached model when its key matches and every resolved jar still exists.
func LoadCache(root, key string) (*Model, bool) {
	data, err := os.ReadFile(cachePath(root))
	if err != nil {
		return nil, false
	}
	var c cacheFile
	if json.Unmarshal(data, &c) != nil || c.Key != key || c.Model == nil {
		return nil, false
	}
	own := map[string]bool{} // a sibling's jar may not be built yet; Subst swaps it for class dirs
	for _, p := range c.Model.Projects {
		if p.Jar != "" {
			own[p.Jar] = true
		}
	}
	for _, p := range c.Model.Projects {
		for _, list := range [][]string{p.Compile, p.Runtime, p.TestCompile, p.TestRuntime, p.Processors} {
			for _, e := range list {
				if strings.HasSuffix(e, ".jar") && !own[e] {
					if _, err := os.Stat(e); err != nil {
						return nil, false
					}
				}
			}
		}
	}
	return c.Model, true
}

// SaveCache stores the model atomically.
func SaveCache(root, key string, m *Model) error {
	if err := os.MkdirAll(filepath.Dir(cachePath(root)), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cacheFile{key, m}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(cachePath(root)), ".gradle-model-*.tmp")
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
	return os.Rename(tmp.Name(), cachePath(root))
}

var allowedPluginPrefixes = []string{
	"org.gradle.api.plugins.", "org.gradle.language.base.plugins.", "org.gradle.api.publish.",
	"org.gradle.testing.jacoco.", "org.gradle.plugins.ide.", "org.gradle.api.reporting.",
}

var deniedPluginPrefixes = []string{
	"org.gradle.api.plugins.WarPlugin", "org.gradle.api.plugins.EarPlugin", "org.gradle.api.plugins.GroovyPlugin",
	"org.gradle.api.plugins.ScalaPlugin", "org.gradle.api.plugins.antlr", "org.gradle.api.plugins.JavaPlatformPlugin",
	"org.gradle.api.plugins.JavaLibraryDistributionPlugin", "org.gradle.api.plugins.DistributionPlugin",
}

func pluginSupported(class string) bool {
	for _, d := range deniedPluginPrefixes {
		if strings.HasPrefix(class, d) {
			return false
		}
	}
	for _, a := range allowedPluginPrefixes {
		if strings.HasPrefix(class, a) {
			return true
		}
	}
	return false
}

var benignCompileDeps = map[string]bool{"compileJava": true, "jar": true, "classes": true, "processResources": true}

// Unsupported lists reasons jv cannot reproduce this build natively. Empty means it can.
func (m *Model) Unsupported() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, p := range m.Projects {
		if !p.Java {
			continue
		}
		for _, pl := range p.Plugins {
			if !pluginSupported(pl) {
				add(fmt.Sprintf("%s applies plugin %s", p.Path, pl))
			}
		}
		if len(p.MainJava) > 1 || len(p.TestJava) > 1 || len(p.MainRes) > 1 || len(p.TestRes) > 1 {
			add(p.Path + " has several source or resource directories")
		}
		if len(p.MainClasses) > 1 || len(p.TestClasses) > 1 {
			add(p.Path + " has several class output directories")
		}
		if len(p.Processors) > 0 {
			add(p.Path + " uses annotation processors")
		}
		if strings.Contains(p.TestFramework, "TestNG") {
			add(p.Path + " uses TestNG")
		}
		if p.Jar == "" {
			add(p.Path + " has no jar task")
		}
		for _, d := range p.CompileDeps {
			if !benignCompileDeps[d] {
				add(fmt.Sprintf("%s: compileJava depends on task %s", p.Path, d))
			}
		}
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	if s == "" {
		return false
	}
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func (p *Project) uses(q *Project) bool {
	if p == q || !q.Java {
		return false
	}
	for _, cp := range [][]string{p.Compile, p.Runtime, p.TestCompile, p.TestRuntime} {
		if contains(cp, q.Jar) {
			return true
		}
		for _, c := range q.MainClasses {
			if contains(cp, c) {
				return true
			}
		}
	}
	return false
}

// Order returns the Java projects with dependencies first.
func (m *Model) Order() ([]*Project, error) {
	var java []*Project
	for i := range m.Projects {
		if m.Projects[i].Java {
			java = append(java, &m.Projects[i])
		}
	}
	state := map[*Project]int{}
	var out []*Project
	var visit func(p *Project, stack []string) error
	visit = func(p *Project, stack []string) error {
		switch state[p] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("dependency cycle between projects: %s -> %s", strings.Join(stack, " -> "), p.Path)
		}
		state[p] = 1
		for _, q := range java {
			if p.uses(q) {
				if err := visit(q, append(stack, p.Path)); err != nil {
					return err
				}
			}
		}
		state[p] = 2
		out = append(out, p)
		return nil
	}
	for _, p := range java {
		if err := visit(p, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (m *Model) rel(p *Project) string {
	r, err := filepath.Rel(m.Root, p.Dir)
	if err != nil {
		return p.Dir
	}
	return filepath.ToSlash(r)
}

// find returns the project named by a path (":app:lib"), a name, or a directory relative to
// the root, or nil.
func (m *Model) find(ordered []*Project, n string) *Project {
	for _, p := range ordered {
		if p.Path == n || p.Path == ":"+strings.TrimPrefix(n, ":") || p.Name == n || m.rel(p) == strings.Trim(filepath.ToSlash(n), "/") {
			return p
		}
	}
	return nil
}

// Only returns exactly the named projects, without the projects they use.
func (m *Model) Only(ordered []*Project, names []string) ([]*Project, error) {
	var out []*Project
	for _, n := range names {
		hit := m.find(ordered, n)
		if hit == nil {
			return nil, fmt.Errorf("no Java project %q (known: %s)", n, m.names(ordered))
		}
		out = append(out, hit)
	}
	return out, nil
}

// Select keeps the named projects (path ":app:lib", "app", project name, or directory
// relative to the root) plus every project they use, preserving build order.
func (m *Model) Select(ordered []*Project, names []string) ([]*Project, error) {
	want := map[*Project]bool{}
	var add func(p *Project)
	add = func(p *Project) {
		if want[p] {
			return
		}
		want[p] = true
		for _, q := range ordered {
			if p.uses(q) {
				add(q)
			}
		}
	}
	for _, n := range names {
		hit := m.find(ordered, n)
		if hit == nil {
			return nil, fmt.Errorf("no Java project %q (known: %s)", n, m.names(ordered))
		}
		add(hit)
	}
	var out []*Project
	for _, p := range ordered {
		if want[p] {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *Model) names(ps []*Project) string {
	var n []string
	for _, p := range ps {
		n = append(n, p.Path)
	}
	return strings.Join(n, ", ")
}

// Subst rewrites a classpath so that the jar of another project in this build is replaced by
// that project's class and resource directories (jv builds them itself; the jar may not exist).
func (m *Model) Subst(cp []string) []string {
	var out []string
	for _, e := range cp {
		replaced := false
		for i := range m.Projects {
			q := &m.Projects[i]
			if q.Java && q.Jar != "" && e == q.Jar {
				out = append(out, q.MainClasses...)
				if q.MainResOut != "" {
					out = append(out, q.MainResOut)
				}
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, e)
		}
	}
	return out
}
