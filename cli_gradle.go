// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jv-build/jv/compiler"
	"github.com/jv-build/jv/config"
	"github.com/jv-build/jv/gradle"
	"github.com/jv-build/jv/packager"
	"github.com/jv-build/jv/resolver"
	"github.com/jv-build/jv/runner"
)

// Goals the Gradle model oracle can serve natively. Everything else goes to Gradle itself.
var gradleGoals = map[string]bool{"clean": true, "compile": true, "test": true, "package": true, "classpath": true}

// findGradleRoot returns the directory holding settings.gradle[.kts] (searching upwards),
// or dir itself for a single-project build.
func findGradleRoot(dir string) string {
	for cur := filepath.Clean(dir); ; {
		for _, n := range []string{"settings.gradle", "settings.gradle.kts"} {
			if _, err := os.Stat(filepath.Join(cur, n)); err == nil {
				return cur
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	for _, n := range []string{"build.gradle", "build.gradle.kts"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			return dir
		}
	}
	return ""
}

// gradleExtraArgs picks the -P/-D arguments that can change a Gradle build's model.
func gradleExtraArgs(args []string) []string {
	var out []string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "-Dtest"), strings.HasPrefix(a, "-DskipTests"), strings.HasPrefix(a, "-Dmaven."):
		case strings.HasPrefix(a, "-P") && len(a) > 2, strings.HasPrefix(a, "-D") && len(a) > 2:
			out = append(out, a)
		}
	}
	return out
}

// runGradleNative builds a Gradle project natively using the model a real Gradle reports.
// handled is false when the caller must delegate to Gradle (bridge), which is always safe.
func runGradleNative(o *cliOpts, args []string, cwd, gradleCmd string) (code int, handled bool) {
	if os.Getenv("JV_GRADLE_ORACLE") == "0" || len(o.unknown) > 0 || len(o.profiles) > 0 {
		return 0, false
	}
	for _, g := range o.goals {
		if !gradleGoals[g] {
			return 0, false
		}
	}
	root := findGradleRoot(cwd)
	if root == "" {
		return 0, false
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	start := time.Now()
	orig, werr := os.Getwd() // restore where the process really was; cwd is just the project to build
	if werr != nil {
		orig = cwd
	}
	defer func() { _ = os.Chdir(orig) }()

	extra := gradleExtraArgs(args)
	key, err := gradle.CacheKey(root, extra)
	if err != nil {
		logf("cannot fingerprint the Gradle build (%v); delegating to gradle", err)
		return 0, false
	}
	model, ok := gradle.LoadCache(root, key)
	if !ok {
		logf("asking Gradle for the project model (cached until a build script changes)...")
		if model, err = gradle.Query(ctx, gradleCmd, root, extra, o.offline); err != nil {
			logf("%v; delegating to gradle", err)
			return 0, false
		}
		if err := gradle.SaveCache(root, key, model); err != nil {
			logf("warning: could not cache the Gradle model: %v", err)
		}
	} else if o.verbose {
		logf("using cached Gradle model")
	}
	if reasons := model.Unsupported(); len(reasons) > 0 && os.Getenv("JV_IGNORE_BUILD_CONFIG") == "" {
		logf("this Gradle build uses features jv does not reproduce natively (%s); delegating to gradle (set JV_IGNORE_BUILD_CONFIG=1 to build natively anyway)", strings.Join(reasons, "; "))
		return 0, false
	}
	ordered, err := model.Order()
	if err != nil {
		// Gradle orders tasks per source set, so a cycle between whole projects (for example app's
		// tests using a plugin that depends on app) is valid there. Let Gradle decide.
		logf("%v; delegating to gradle", err)
		return 0, false
	}
	names := o.pl
	if len(names) == 0 && filepath.Clean(cwd) != filepath.Clean(root) { // run inside a subproject
		for _, p := range ordered {
			if filepath.Clean(p.Dir) == filepath.Clean(cwd) {
				names = []string{p.Path}
			}
		}
		if len(names) == 0 {
			return 0, false
		}
	}
	targets := ordered
	if len(names) > 0 {
		if targets, err = model.Select(ordered, names); err != nil {
			errf("%v", err)
			return 1, true
		}
	}
	if len(targets) == 0 {
		logf("no Java projects in this Gradle build; delegating to gradle")
		return 0, false
	}
	if o.verbose {
		logf("building natively with the Gradle model (%d project(s))", len(targets))
	}

	var loader *resolver.Loader
	mk := func() *resolver.Loader {
		if loader == nil {
			settings, _ := config.LoadSettings(o.settingsFile)
			if settings == nil {
				settings = &config.Settings{}
			}
			loader = resolver.NewLoader(ctx, settings.RepoDir(), settings)
			loader.Offline, loader.Verbose = o.offline, o.verbose
		}
		return loader
	}

	for _, goal := range o.goals {
		switch goal {
		case "clean":
			for _, p := range targets { // check all first: delete nothing if any path looks wrong
				if !safeToClean(p, root) {
					errf("%s: refusing to delete build directory %q because it contains the project or its sources; use gradle clean", p.Name, p.BuildDir)
					return 1, true
				}
			}
			for _, p := range targets {
				_ = os.RemoveAll(p.BuildDir)
			}
			logf("cleaned %d project(s)", len(targets))
		case "classpath":
			sel := targets // -pl pulls in the projects a project uses; the classpath is only for the named one
			if len(names) > 0 {
				var err error
				if sel, err = model.Only(ordered, names); err != nil {
					errf("%v", err)
					return 1, true
				}
			}
			if len(sel) != 1 {
				errf("classpath needs exactly one Java project; run it inside a project directory or select one with -pl")
				return 1, true
			}
			p := sel[0]
			list := p.TestRuntime
			switch o.scope {
			case "compile":
				list = p.Compile
			case "runtime":
				list = p.Runtime
			}
			fmt.Println(strings.Join(model.Subst(list), string(os.PathListSeparator)))
		default:
			for _, p := range targets {
				if err := buildGradleProject(ctx, mk, model, p, goal, o, len(targets) > 1); err != nil {
					if ctx.Err() != nil {
						errf("interrupted")
					} else {
						errf("%s: %v", p.Name, err)
					}
					return 1, true
				}
			}
		}
	}
	logf("done in %v", time.Since(start).Round(time.Millisecond))
	return 0, true
}

func nonEmpty(in ...string) []string {
	var out []string
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func buildGradleProject(ctx context.Context, mk func() *resolver.Loader, model *gradle.Model, p *gradle.Project, goal string, o *cliOpts, multi bool) error {
	mlog := func(format string, a ...any) {
		if multi {
			logf("["+p.Name+"] "+format, a...)
		} else {
			logf(format, a...)
		}
	}
	if err := os.Chdir(p.Dir); err != nil {
		return err
	}
	opts := compiler.Options{Encoding: p.Encoding, Release: p.Release, Source: p.Source, Target: p.Target, Args: p.CompilerArgs}
	if opts.Encoding == "" {
		opts.Encoding = "UTF-8"
	}
	if len(p.MainClasses) != 1 {
		return fmt.Errorf("expected one main class directory, Gradle reported %d", len(p.MainClasses))
	}
	mainOut := p.MainClasses[0]

	if len(p.MainJava) == 1 {
		compiled, err := compiler.Compile(ctx, p.MainJava[0], mainOut, model.Subst(p.Compile), opts)
		if err != nil {
			return fmt.Errorf("compilation failed: %w", err)
		}
		if compiled {
			mlog("compiled main sources")
		} else {
			mlog("main classes up to date")
		}
	}
	if len(p.MainRes) == 1 && p.MainResOut != "" {
		if err := compiler.CopyResources(p.MainRes[0], p.MainResOut); err != nil {
			return fmt.Errorf("copying resources: %w", err)
		}
	}
	if goal == "compile" {
		return nil
	}

	if !o.skipTestCompilation && len(p.TestJava) == 1 && len(p.TestClasses) == 1 {
		testOut := p.TestClasses[0]
		own := nonEmpty(testOut, p.TestResOut, mainOut, p.MainResOut)
		tc, err := compiler.Compile(ctx, p.TestJava[0], testOut, append(append([]string{}, own...), model.Subst(p.TestCompile)...), opts)
		if err != nil {
			return fmt.Errorf("test compilation failed: %w", err)
		}
		if len(p.TestRes) == 1 && p.TestResOut != "" {
			if err := compiler.CopyResources(p.TestRes[0], p.TestResOut); err != nil {
				return fmt.Errorf("copying test resources: %w", err)
			}
		}
		if tc {
			mlog("compiled test sources")
		}
		if !o.skipTests && p.TestEnabled {
			if st, err := os.Stat(testOut); err == nil && st.IsDir() {
				runCp := append(append([]string{}, own...), model.Subst(p.TestRuntime)...)
				launcher := ""
				if !hasLauncherPath(runCp) {
					ver := os.Getenv("JV_JUNIT_VERSION")
					if ver == "" {
						ver = defaultJUnit
					}
					if launcher, err = mk().FetchJar("org.junit.platform", "junit-platform-console-standalone", ver); err != nil {
						return fmt.Errorf("fetching JUnit launcher: %w", err)
					}
				}
				cfg := runner.Config{
					Scan:     testOut,
					Reports:  filepath.Join(p.BuildDir, "test-results", "test"),
					JVMArgs:  p.JvmArgs,
					SysProps: p.SystemProps,
				}
				if err := runner.RunTestsIn(ctx, cfg, launcher, runCp, o.props["test"]); err != nil {
					switch {
					case errors.Is(err, runner.ErrTestsFailed) && o.props["maven.test.failure.ignore"] == "true":
						mlog("warning: test failures ignored (maven.test.failure.ignore=true)")
					case errors.Is(err, runner.ErrTestsFailed):
						return err
					default:
						return fmt.Errorf("running tests: %w", err)
					}
				}
			}
		}
	}
	if goal == "test" {
		return nil
	}

	// package: Gradle keeps classes and resources apart; the jar needs both.
	stage, err := os.MkdirTemp("", "jv-jar-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := compiler.CopyResources(mainOut, stage); err != nil {
		return err
	}
	if p.MainResOut != "" {
		if err := compiler.CopyResources(p.MainResOut, stage); err != nil {
			return err
		}
	}
	if err := packager.Jar(stage, p.Jar, manifestAttrs(p.Manifest)); err != nil {
		return fmt.Errorf("packaging: %w", err)
	}
	mlog("built %s", p.Jar)
	return nil
}

func hasLauncherPath(cp []string) bool {
	for _, e := range cp {
		if strings.HasPrefix(filepath.Base(e), "junit-platform-console") {
			return true
		}
	}
	return false
}

// manifestAttrs orders manifest headers deterministically with Main-Class first.
func manifestAttrs(m map[string]string) [][2]string {
	keys := make([]string, 0, len(m))
	for k := range m {
		if k != "Manifest-Version" {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if (keys[i] == "Main-Class") != (keys[j] == "Main-Class") {
			return keys[i] == "Main-Class"
		}
		return keys[i] < keys[j]
	})
	out := make([][2]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, [2]string{k, m[k]})
	}
	return out
}

// within reports whether child is parent itself or lies inside it.
func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// safeToClean refuses a build directory that is, or contains, the project, the build root or
// any source directory. The path comes from the build script, and a mistake there (for
// example buildDir = projectDir) must not turn clean into deleting source code.
func safeToClean(p *gradle.Project, root string) bool {
	if p.BuildDir == "" || within(p.BuildDir, p.Dir) || within(p.BuildDir, root) {
		return false
	}
	for _, dirs := range [][]string{p.MainJava, p.MainRes, p.TestJava, p.TestRes} {
		for _, d := range dirs {
			if within(p.BuildDir, d) {
				return false
			}
		}
	}
	return true
}
