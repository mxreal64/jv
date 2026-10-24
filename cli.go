// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mxreal64/jv/bridge"
	"github.com/mxreal64/jv/cmd"
	"github.com/mxreal64/jv/compiler"
	"github.com/mxreal64/jv/config"
	"github.com/mxreal64/jv/lifecycle"
	"github.com/mxreal64/jv/packager"
	"github.com/mxreal64/jv/plugins/codegen"
	"github.com/mxreal64/jv/plugins/enforcer"
	"github.com/mxreal64/jv/plugins/flatten"
	"github.com/mxreal64/jv/plugins/jacoco"
	"github.com/mxreal64/jv/plugins/shade"
	"github.com/mxreal64/jv/plugins/springboot"
	"github.com/mxreal64/jv/pom"
	"github.com/mxreal64/jv/resolver"
	"github.com/mxreal64/jv/runner"
)

var (
	version = "v0.2.1-rc1"
	quiet   bool
)

const defaultJUnit = "1.10.2"

const usage = `jv - a fast build tool for Java projects (Maven & Gradle compatible)

Usage: jv [flags] <goal>...

Maven Goals:
compile     resolve dependencies and compile main sources
test        compile and run JUnit Platform tests
package     test and build target/<artifactId>-<version>.jar
install     package and copy into the local repository
clean       remove target/ and .jv/
classpath [compile|runtime|test]   print the resolved classpath (default: test)
tree        print the resolved dependency tree
Any other goal or plugin is delegated transparently to mvn (or Gradle if in a Gradle project).
In a Gradle project jv asks Gradle once for the project model (cached in .jv/) and then compiles,
tests and packages natively; builds it cannot reproduce (Kotlin, annotation processors, custom
tasks on compileJava, ...) run through Gradle. JV_GRADLE_ORACLE=0 disables the native path.

Flags:
-o, --offline            never touch the network
-q, --quiet              only print errors
-X, --verbose            log every download and verbose details
-P ids                   activate profiles (comma-separated, !id deactivates)
-D key[=value]           set a property (-DskipTests, -Dtest=MyTest)
-pl, --projects modules  build only these modules (+ the modules they need)
-s, --settings file      path to alternative settings.xml
-U, --update-snapshots   re-check remote -SNAPSHOT dependencies
-B -e -ntp -V -am        accepted and ignored (Maven compatibility; -pl already builds what a module needs)
--version, -h

Any other flag, goal or plugin is delegated to mvn untouched.
If a POM customises the build in ways jv cannot reproduce (sourceDirectory, finalName, filtered
resources, compiler args/annotation processors, plugin executions such as shade) jv delegates to mvn;
set JV_IGNORE_BUILD_CONFIG=1 to build natively anyway (a warning is printed).
`

var nativeGoals = map[string]bool{
	"clean":     true,
	"compile":   true,
	"test":      true,
	"package":   true,
	"install":   true,
	"classpath": true,
	"tree":      true,
	"lsp":       true,
}

type cliOpts struct {
	goals                          []string
	scope                          string
	settingsFile                   string
	offline, verbose, quiet        bool
	help, version                  bool
	profiles                       map[string]bool
	props                          map[string]string
	pl                             []string
	unknown                        []string
	skipTests, skipTestCompilation bool
	updateSnapshots                bool
}

func parseArgs(args []string) *cliOpts {
	o := &cliOpts{profiles: map[string]bool{}, props: map[string]string{}, scope: "test"}
	split := func(s string) (out []string) {
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return
	}
	addProfiles := func(s string) {
		for _, p := range split(s) {
			o.profiles[p] = true
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-o" || a == "--offline":
			o.offline = true
		case a == "--verbose" || a == "-X":
			o.verbose = true
		case a == "-q" || a == "--quiet":
			o.quiet = true
		case a == "-h" || a == "--help":
			o.help = true
		case a == "--version":
			o.version = true
		case (a == "-s" || a == "--settings") && i+1 < len(args):
			i++
			o.settingsFile = args[i]
		case (a == "-P") && i+1 < len(args):
			i++
			addProfiles(args[i])
		case (a == "-pl" || a == "--projects") && i+1 < len(args):
			i++
			o.pl = append(o.pl, split(args[i])...)
		case strings.HasPrefix(a, "-P") && len(a) > 2:
			addProfiles(a[2:])
		case a == "-D" && i+1 < len(args):
			i++
			k, v := args[i], "true"
			if j := strings.Index(k, "="); j >= 0 {
				k, v = k[:j], k[j+1:]
			}
			o.props[k] = v
		case strings.HasPrefix(a, "-D") && len(a) > 2:
			k, v := a[2:], "true"
			if j := strings.Index(k, "="); j >= 0 {
				k, v = k[:j], k[j+1:]
			}
			o.props[k] = v
		case a == "-U" || a == "--update-snapshots":
			o.updateSnapshots = true
		case a == "-B" || a == "--batch-mode" || a == "-e" || a == "--errors" ||
			a == "-ntp" || a == "--no-transfer-progress" || a == "-V" || a == "--show-version" ||
			a == "-am" || a == "--also-make":
			// accepted for Maven compatibility; no effect
		case strings.HasPrefix(a, "-"):
			o.unknown = append(o.unknown, a)
		default:
			o.goals = append(o.goals, a)
			if a == "classpath" && i+1 < len(args) && (args[i+1] == "compile" || args[i+1] == "runtime" || args[i+1] == "test") {
				i++
				o.scope = args[i]
			}
		}
	}
	o.skipTestCompilation = o.props["maven.test.skip"] == "true"
	o.skipTests = o.skipTestCompilation || o.props["skipTests"] == "true"
	return o
}

func logf(format string, a ...any) {
	if !quiet {
		fmt.Fprintf(os.Stderr, "[jv] "+format+"\n", a...)
	}
}

func errf(format string, a ...any) { fmt.Fprintf(os.Stderr, "[jv] error: "+format+"\n", a...) }

func run(args []string) int {
	o := parseArgs(args)
	quiet = o.quiet
	if o.version {
		fmt.Println("jv " + version)
		fmt.Println("Copyright mxreal64, 2026")
		fmt.Println("License GPLv3: GNU GPL version 3 <https://www.gnu.org/licenses/gpl-3.0.html>.")
		fmt.Println("This is free software: you are free to change and redistribute it.")
		fmt.Println("There is NO WARRANTY, to the extent permitted by law.")
		return 0
	}
	if o.help || len(o.goals) == 0 {
		fmt.Print(usage)
		if o.help {
			return 0
		}
		return 2
	}

	root, err := os.Getwd()
	if err != nil {
		errf("%v", err)
		return 1
	}
	defer func() { _ = os.Chdir(root) }() // builds chdir into modules; never leave the process there

	hasPom := false
	if _, err := os.Stat(filepath.Join(root, "pom.xml")); err == nil {
		hasPom = true
	}

	isGradle, gradlew := bridge.DetectGradle(root)

	// jv lsp — JSON-RPC classpath server; handled before project-type detection.
	if len(o.goals) > 0 && o.goals[0] == "lsp" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := cmd.RunLSP(ctx, root, os.Stdin, os.Stdout); err != nil && err != context.Canceled {
			errf("lsp: %v", err)
			return 1
		}
		return 0
	}

	// If no pom.xml is found, but Gradle project is detected, delegate to Gradle
	if !hasPom && isGradle {
		// Model-oracle mode: a real Gradle reports the project model once (cached), jv builds
		// natively from it. Anything it cannot reproduce falls back to running Gradle itself.
		if code, handled := runGradleNative(o, args, root, gradlew); handled {
			return code
		}
		logf("Gradle project detected (%s); running via gradle", filepath.Base(gradlew))
		return bridge.FallbackToGradle(args)
	}

	// For unsupported goals or flags, delegate to mvn (or gradle if gradle-only)
	for _, g := range o.goals {
		if !nativeGoals[g] {
			if !hasPom && isGradle {
				return bridge.FallbackToGradle(args)
			}
			return bridge.FallbackToMaven(args)
		}
	}
	if len(o.unknown) > 0 {
		if !hasPom && isGradle {
			return bridge.FallbackToGradle(args)
		}
		logf("flag(s) %s not handled natively; delegating to mvn", strings.Join(o.unknown, " "))
		return bridge.FallbackToMaven(args)
	}

	if _, err := pom.ParsePOM(filepath.Join(root, "pom.xml")); err != nil {
		if isGradle {
			logf("no usable pom.xml; delegating to Gradle")
			return bridge.FallbackToGradle(args)
		}
		logf("no usable pom.xml (%v); delegating to mvn", err)
		return bridge.FallbackToMaven(args)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer compiler.ShutdownWorker()
	start := time.Now()

	settings, err := config.LoadSettings(o.settingsFile)
	if err != nil {
		logf("ignoring unreadable settings.xml: %v", err)
	}
	for _, ap := range settings.ActiveProfiles {
		if !o.profiles["!"+ap] {
			o.profiles[ap] = true
		}
	}
	// Properties of active settings.xml profiles act like defaults for -D.
	for k, v := range settings.ActiveProps(o.profiles) {
		if _, set := o.props[k]; !set {
			o.props[k] = v
		}
	}

	dirs, err := resolver.ListModuleDirs(root, o.profiles, o.props)
	if err != nil {
		errf("%v", err)
		return 1
	}
	// clean is positional: leading clean (no -pl) runs before module loading for speed.
	// clean anywhere else in the goal list runs in the build loop at its position.
	build := append([]string(nil), o.goals...)
	if len(build) > 0 && build[0] == "clean" && len(o.pl) == 0 {
		for _, d := range dirs {
			_ = os.RemoveAll(filepath.Join(d, "target"))
			_ = os.RemoveAll(filepath.Join(d, ".jv"))
		}
		logf("cleaned %d module(s)", len(dirs))
		build = build[1:]
	}
	if len(build) == 0 {
		return 0
	}

	loader := resolver.NewLoader(ctx, settings.RepoDir(), settings)
	loader.Offline, loader.Verbose, loader.Quiet, loader.ProfileIDs, loader.UserProps = o.offline, o.verbose, o.quiet, o.profiles, o.props
	loader.UpdateSnapshots = o.updateSnapshots
	loader.AddSettingsRepos(settings.ActiveRepos(o.profiles))
	mods, err := loader.Discover(root)
	if err != nil {
		errf("cannot build effective POM: %v", err)
		return 1
	}
	for _, m := range mods {
		if p := m.Eff.Packaging; p != "jar" && p != "pom" {
			logf("module %s uses packaging %q, which is not handled natively; delegating to mvn", m.Eff.A, p)
			return bridge.FallbackToMaven(args)
		}
	}
	needsBuild := false
	for _, g := range build {
		if g != "clean" && g != "classpath" && g != "tree" {
			needsBuild = true
		}
	}
	for _, m := range mods {
		if len(m.Eff.Unsupported) == 0 {
			continue
		}
		msg := fmt.Sprintf("module %s uses <build> settings jv does not honour: %s", m.Eff.A, strings.Join(m.Eff.Unsupported, ", "))
		if needsBuild && os.Getenv("JV_IGNORE_BUILD_CONFIG") == "" {
			logf("%s; delegating to mvn (set JV_IGNORE_BUILD_CONFIG=1 to build natively anyway)", msg)
			return bridge.FallbackToMaven(args)
		}
		logf("warning: %s", msg)
	}
	if o.verbose {
		logf("building natively (%d module(s))", len(mods))
	}
	loader.Register(mods)
	ordered, err := resolver.Order(mods)
	if err == nil && len(o.pl) > 0 {
		ordered, err = resolver.Select(ordered, root, o.pl)
	}
	if err != nil {
		errf("%v", err)
		return 1
	}
	for _, goal := range build {
		if goal == "clean" { // a clean that is not the first goal (or any clean with -pl) runs here, at its position
			sel := ordered
			if len(o.pl) > 0 {
				var serr error
				if sel, serr = resolver.SelectOnly(ordered, root, o.pl); serr != nil {
					errf("%v", serr)
					return 1
				}
			}
			for _, m := range sel {
				_ = os.RemoveAll(filepath.Join(m.Dir, "target"))
				_ = os.RemoveAll(filepath.Join(m.Dir, ".jv"))
			}
			logf("cleaned %d module(s)", len(sel))
			continue
		}
		targets := ordered
		if (goal == "classpath" || goal == "tree") && len(o.pl) > 0 {
			if targets, err = resolver.SelectOnly(ordered, root, o.pl); err != nil {
				errf("%v", err)
				return 1
			}
		}
		if goal == "classpath" {
			jars := 0
			for _, m := range targets {
				if m.Eff.Packaging == "jar" {
					jars++
				}
			}
			if jars != 1 {
				errf("classpath needs exactly one jar module; run it inside a module directory or select one with -pl")
				return 1
			}
		}
		if len(targets) > 1 && (goal == "compile" || goal == "test-compile") {
			byGAV := make(map[string]*resolver.Module, len(targets))
			byGA := make(map[string]*resolver.Module, len(targets))
			for _, m := range targets {
				byGAV[m.GAV()] = m
				byGA[m.Eff.G+":"+m.Eff.A] = m
			}
			dependents := make(map[*resolver.Module][]*resolver.Module, len(targets))
			inDegree := make(map[*resolver.Module]int, len(targets))
			for _, m := range targets {
				depsSeen := make(map[*resolver.Module]bool)
				if m.Eff != nil {
					for _, d := range m.Eff.Deps {
						var depMod *resolver.Module
						if dm := byGAV[d.GroupID+":"+d.ArtifactID+":"+d.Version]; dm != nil && dm != m {
							depMod = dm
						} else if dm := byGA[d.GroupID+":"+d.ArtifactID]; dm != nil && dm != m {
							depMod = dm
						}
						if depMod != nil && !depsSeen[depMod] {
							depsSeen[depMod] = true
							dependents[depMod] = append(dependents[depMod], m)
							inDegree[m]++
						}
					}
				}
			}
			workers := len(targets)
			if n := runtime.NumCPU(); workers > n {
				workers = n
			}
			if workers < 1 {
				workers = 1
			}
			ready := make(chan *resolver.Module, len(targets))
			for _, m := range targets {
				if inDegree[m] == 0 {
					ready <- m
				}
			}
			var mu sync.Mutex
			var firstErr error
			completed := 0
			var wg sync.WaitGroup
			wg.Add(workers)
			rctx, rcancel := context.WithCancel(ctx)
			defer rcancel()
			for i := 0; i < workers; i++ {
				go func() {
					defer wg.Done()
					for {
						select {
						case <-rctx.Done():
							return
						case m, ok := <-ready:
							if !ok {
								return
							}
							err := buildModule(rctx, loader, m, goal, o, true)
							mu.Lock()
							if err != nil {
								if firstErr == nil {
									firstErr = err
									rcancel()
								}
								mu.Unlock()
								return
							}
							completed++
							for _, dep := range dependents[m] {
								inDegree[dep]--
								if inDegree[dep] == 0 {
									ready <- dep
								}
							}
							if completed == len(targets) {
								close(ready)
							}
							mu.Unlock()
						}
					}
				}()
			}
			wg.Wait()
			if firstErr != nil {
				if ctx.Err() != nil {
					errf("interrupted")
				} else {
					errf("%v", firstErr)
				}
				return 1
			}
		} else {
			for _, m := range targets {
				if err := buildModule(ctx, loader, m, goal, o, len(targets) > 1); err != nil {
					if ctx.Err() != nil {
						errf("interrupted")
					} else {
						errf("%s: %v", m.Eff.A, err)
					}
					return 1
				}
			}
		}
	}
	logf("done in %v", time.Since(start).Round(time.Millisecond))
	return 0
}

func resolveModule(loader *resolver.Loader, m *resolver.Module, forTree bool, mlog func(string, ...any)) ([]resolver.Artifact, []*resolver.Node, error) {
	const lock = ".jv/lock.json"
	if !forTree && !loader.UpdateSnapshots {
		if cached, ok := resolver.ReadLock(lock, m.Key); ok {
			mlog("using cached resolution (%d artifacts)", len(cached))
			return cached, nil, nil
		}
	}
	t := time.Now()
	mlog("resolving dependencies...")
	nodes, err := loader.Resolve(m.Eff)
	if err != nil {
		return nil, nil, fmt.Errorf("resolution: %w", err)
	}
	if forTree {
		return nil, nodes, nil
	}
	arts, err := loader.Artifacts(resolver.Flatten(nodes))
	if err != nil {
		return nil, nil, fmt.Errorf("download: %w", err)
	}
	if err := resolver.WriteLock(lock, m.Key, arts); err != nil {
		mlog("warning: could not write lock: %v", err)
	}
	mlog("resolved %d artifacts in %v", len(arts), time.Since(t).Round(time.Millisecond))
	return arts, nil, nil
}

// makeBuildContext constructs a BuildContext from a resolved module and its artifacts.
func makeBuildContext(m *resolver.Module, arts []resolver.Artifact, loader *resolver.Loader, effProps map[string]string) lifecycle.BuildContext {
	// Build PluginConfigs map from the effective build.
	pluginConfigs := map[string]pom.PluginConfig{}
	for _, p := range m.Eff.Build.EffectivePlugins() {
		pluginConfigs[p.ArtifactID] = p.Configuration
	}
	filtering := false
	for _, r := range m.Eff.Build.Resources {
		if strings.TrimSpace(r.Filtering) == "true" {
			filtering = true
			break
		}
	}
	testFiltering := false
	for _, r := range m.Eff.Build.TestResources {
		if strings.TrimSpace(r.Filtering) == "true" {
			testFiltering = true
			break
		}
	}

	return lifecycle.BuildContext{
		Group:                 m.Eff.G,
		Artifact:              m.Eff.A,
		Version:               m.Eff.V,
		Packaging:             m.Eff.Packaging,
		Dir:                   m.Dir,
		SourceDirs:            []string{"src/main/java"},
		TestSourceDirs:        []string{"src/test/java"},
		ResourceDirs:          []string{"src/main/resources"},
		TestResourceDirs:      []string{"src/test/resources"},
		ResourceFiltering:     filtering,
		TestResourceFiltering: testFiltering,
		ClassesDir:            "target/classes",
		TestClassesDir:        "target/test-classes",
		GeneratedDir:          "target/generated-sources/annotations",
		TargetDir:             "target",
		CompileCP:             resolver.Classpath(arts, "compile"),
		TestCP:                resolver.Classpath(arts, "test"),
		RuntimeCP:             resolver.Classpath(arts, "runtime"),
		Props:                 effProps,
		PluginConfigs:         pluginConfigs,
		Repo:                  loader.Repo,
	}
}

// buildLifecycle constructs the default Maven lifecycle for a jar/war module.
func buildLifecycle(
	ctx context.Context,
	loader *resolver.Loader,
	m *resolver.Module,
	arts []resolver.Artifact,
	o *cliOpts,
	mlog func(string, ...any),
) *lifecycle.Lifecycle {
	lc := lifecycle.New()

	// validate
	lc.Add(lifecycle.FuncExecutor{PhaseVal: "validate", IDVal: "enforcer", Fn: func(ctx context.Context, bc lifecycle.BuildContext) error {
		if _, ok := bc.PluginConfigs["maven-enforcer-plugin"]; ok {
			if err := enforcer.RunEnforcer(bc, arts, bc.Props["java.version"]); err != nil {
				return fmt.Errorf("enforcer error: %w", err)
			}
			mlog("validated enforcer rules")
		}
		return nil
	}})

	// initialize
	lc.Add(lifecycle.FuncExecutor{PhaseVal: "initialize", IDVal: "jacoco-prepare", Fn: func(ctx context.Context, bc lifecycle.BuildContext) error {
		if _, ok := bc.PluginConfigs["jacoco-maven-plugin"]; ok {
			if err := jacoco.PrepareAgent(&bc); err != nil {
				return fmt.Errorf("jacoco prepare: %w", err)
			}
			mlog("prepared jacoco javaagent")
		}
		return nil
	}})

	// generate-sources
	lc.Add(lifecycle.FuncExecutor{PhaseVal: "generate-sources", IDVal: "codegen", Fn: func(ctx context.Context, bc lifecycle.BuildContext) error {
		// Auto-detect protobuf / antlr4
		if _, ok := bc.PluginConfigs["protobuf-maven-plugin"]; ok {
			g := codegen.Generator{
				ID:        "protobuf",
				Phase:     "generate-sources",
				Command:   []string{"protoc", "--java_out=" + filepath.Join(bc.TargetDir, "generated-sources", "protobuf"), "src/main/proto/*.proto"},
				OutputDir: filepath.Join(bc.TargetDir, "generated-sources", "protobuf"),
				Inputs:    []string{"src/main/proto/**/*.proto"},
			}
			_ = codegen.Run(ctx, g, &bc)
		}
		return nil
	}})

	// process-resources
	lc.Add(lifecycle.FuncExecutor{PhaseVal: "process-resources", IDVal: "resources", Fn: func(ctx context.Context, bc lifecycle.BuildContext) error {
		for _, r := range bc.ResourceDirs {
			if err := compiler.CopyResources(ctx, r, bc.ClassesDir, bc.ResourceFiltering, bc.Props, nil); err != nil {
				return fmt.Errorf("copying resources: %w", err)
			}
		}

		// task 1.2: flatten-maven-plugin check
		cfg, hasFlatten := bc.PluginConfigs["flatten-maven-plugin"]
		if !hasFlatten {
			cfg, hasFlatten = bc.PluginConfigs["maven-flatten-plugin"]
		}
		if hasFlatten {
			modeStr := cfg.GetOther("flattenMode")
			flattenMode := flatten.FlattenMode(modeStr)
			if flattenMode == "" {
				flattenMode = flatten.ModeResolveCiFriendlyVersions
			}
			outFlatten := filepath.Join(bc.Dir, ".flattened-pom.xml")
			if err := flatten.Flatten(m.POMFile, bc.Props, flattenMode, outFlatten); err != nil {
				return fmt.Errorf("flatten pom: %w", err)
			}
			mlog("generated .flattened-pom.xml")
		}

		return nil
	}})

	// compile
	lc.Add(lifecycle.FuncExecutor{PhaseVal: "compile", IDVal: "compile", Fn: func(ctx context.Context, bc lifecycle.BuildContext) error {
		opts := compiler.OptionsFrom(bc.Props)
		var src string
		if dirs := bc.AllSourceDirs(); len(dirs) > 0 {
			src = dirs[0]
		}
		compiled, err := compiler.Compile(ctx, src, bc.ClassesDir, bc.CompileCP, opts)
		if err != nil {
			return fmt.Errorf("compilation failed: %w", err)
		}
		if compiled {
			mlog("compiled main sources")
		} else {
			mlog("main classes up to date")
		}
		return nil
	}})

	// process-test-resources
	lc.Add(lifecycle.FuncExecutor{PhaseVal: "process-test-resources", IDVal: "test-resources", Fn: func(ctx context.Context, bc lifecycle.BuildContext) error {
		if o.skipTestCompilation {
			return nil
		}
		for _, r := range bc.TestResourceDirs {
			if err := compiler.CopyResources(ctx, r, bc.TestClassesDir, bc.TestResourceFiltering, bc.Props, nil); err != nil {
				return fmt.Errorf("copying test resources: %w", err)
			}
		}
		return nil
	}})

	// test-compile
	lc.Add(lifecycle.FuncExecutor{PhaseVal: "test-compile", IDVal: "test-compile", Fn: func(ctx context.Context, bc lifecycle.BuildContext) error {
		if o.skipTestCompilation {
			return nil
		}
		opts := compiler.OptionsFrom(bc.Props)
		var testSrc string
		if dirs := bc.AllTestSourceDirs(); len(dirs) > 0 {
			testSrc = dirs[0]
		}
		testCp := append([]string{bc.TestClassesDir, bc.ClassesDir}, bc.TestCP...)
		tc, err := compiler.Compile(ctx, testSrc, bc.TestClassesDir, testCp, opts)
		if err != nil {
			return fmt.Errorf("test compilation failed: %w", err)
		}
		if tc {
			mlog("compiled test sources")
		}
		return nil
	}})

	// test
	lc.Add(lifecycle.FuncExecutor{PhaseVal: "test", IDVal: "test", Fn: func(ctx context.Context, bc lifecycle.BuildContext) error {
		if o.skipTestCompilation || o.skipTests {
			return nil
		}
		if st, err := os.Stat(bc.TestClassesDir); err != nil || !st.IsDir() {
			return nil
		}
		testCp := append([]string{bc.TestClassesDir, bc.ClassesDir}, bc.TestCP...)
		launcher := ""
		if !hasLauncher(arts) {
			ver := os.Getenv("JV_JUNIT_VERSION")
			if ver == "" {
				ver = defaultJUnit
			}
			var err error
			if launcher, err = loader.FetchJar("org.junit.platform", "junit-platform-console-standalone", ver); err != nil {
				return fmt.Errorf("fetching JUnit launcher: %w", err)
			}
		}
		sfCfg := m.Eff.Build.SurefireConfig()
		runCfg := runner.Config{
			JVMArgs:        jvmArgsFromArgLine(sfCfg.ArgLine, bc.Props),
			SysProps:       sfCfg.SysProps,
			Groups:         sfCfg.Groups,
			ExcludedGroups: sfCfg.ExcludedGroups,
		}
		runCfg.Includes, runCfg.Excludes = surefirePatterns(sfCfg)
		if err := runner.RunTestsIn(ctx, runCfg, launcher, testCp, o.props["test"]); err != nil {
			switch {
			case errors.Is(err, runner.ErrTestsFailed) && o.props["maven.test.failure.ignore"] == "true":
				mlog("warning: test failures ignored (maven.test.failure.ignore=true)")
			case errors.Is(err, runner.ErrTestsFailed):
				return err
			default:
				return fmt.Errorf("running tests: %w", err)
			}
		}
		return nil
	}})

	// package (jar)
	lc.Add(lifecycle.FuncExecutor{PhaseVal: "package", IDVal: "jar", Fn: func(ctx context.Context, bc lifecycle.BuildContext) error {
		jar := filepath.Join(bc.TargetDir, bc.Artifact+"-"+bc.Version+".jar")
		var attrs [][2]string
		for _, k := range []string{"jv.mainClass", "exec.mainClass", "mainClass"} {
			if mc := bc.Props[k]; mc != "" {
				attrs = append(attrs, [2]string{"Main-Class", mc})
				break
			}
		}
		if err := packager.Jar(bc.ClassesDir, jar, attrs); err != nil {
			return fmt.Errorf("packaging: %w", err)
		}
		mlog("built %s", jar)

		// spring-boot:repackage check
		sbCfg, hasSB := bc.PluginConfigs["spring-boot-maven-plugin"]
		if hasSB {
			mainClass := sbCfg.GetOther("mainClass")
			if mainClass == "" {
				for _, k := range []string{"start-class", "startClass", "mainClass", "exec.mainClass", "jv.mainClass"} {
					if mc := bc.Props[k]; mc != "" {
						mainClass = mc
						break
					}
				}
			}
			sbVer := bc.Props["spring-boot.version"]
			if err := springboot.Repackage(jar, bc.RuntimeCP, mainClass, sbVer, jar); err != nil {
				return fmt.Errorf("spring-boot repackage: %w", err)
			}
			mlog("repackaged %s with spring-boot-maven-plugin", jar)
		}

		// task 1.4: maven-shade-plugin:shade check
		_, hasShade := bc.PluginConfigs["maven-shade-plugin"]
		if hasShade {
			var inputs []string
			if _, err := os.Stat(jar); err == nil {
				inputs = append(inputs, jar)
			}
			for _, cp := range bc.RuntimeCP {
				if strings.HasSuffix(cp, ".jar") {
					inputs = append(inputs, cp)
				}
			}
			if err := shade.Merge(inputs, jar, nil, nil, nil); err != nil {
				return fmt.Errorf("shading jar: %w", err)
			}
			mlog("shaded %s with maven-shade-plugin", jar)
		}

		return nil
	}})

	// verify
	lc.Add(lifecycle.FuncExecutor{PhaseVal: "verify", IDVal: "jacoco-report", Fn: func(ctx context.Context, bc lifecycle.BuildContext) error {
		if _, ok := bc.PluginConfigs["jacoco-maven-plugin"]; ok {
			if err := jacoco.GenerateReport(ctx, bc); err != nil {
				return fmt.Errorf("jacoco report: %w", err)
			}
			mlog("generated jacoco report")
		}
		return nil
	}})

	// install
	lc.Add(lifecycle.FuncExecutor{PhaseVal: "install", IDVal: "install", Fn: func(ctx context.Context, bc lifecycle.BuildContext) error {
		jar := filepath.Join(bc.TargetDir, bc.Artifact+"-"+bc.Version+".jar")
		if _, err := os.Stat(jar); err != nil {
			jar = "" // pom-only or no jar yet
		}
		pomToInstall := m.POMFile
		flattened := filepath.Join(bc.Dir, ".flattened-pom.xml")
		if _, err := os.Stat(flattened); err == nil {
			pomToInstall = flattened
		}
		if err := packager.Install(bc.Repo, bc.Group, bc.Artifact, bc.Version, jar, pomToInstall); err != nil {
			return fmt.Errorf("install: %w", err)
		}
		mlog("installed %s:%s:%s into %s", bc.Group, bc.Artifact, bc.Version, bc.Repo)
		return nil
	}})

	return lc
}

func buildModule(ctx context.Context, loader *resolver.Loader, m *resolver.Module, goal string, o *cliOpts, multi bool) error {
	if err := os.Chdir(m.Dir); err != nil {
		return err
	}
	mlog := func(format string, a ...any) {
		if multi {
			logf("["+m.Eff.A+"] "+format, a...)
		} else {
			logf(format, a...)
		}
	}
	if m.Eff.Packaging == "pom" {
		if goal == "install" {
			mlog("installing pom")
			return packager.Install(loader.Repo, m.Eff.G, m.Eff.A, m.Eff.V, "", m.POMFile)
		}
		return nil
	}
	arts, nodes, err := resolveModule(loader, m, goal == "tree", mlog)
	if err != nil {
		return err
	}
	// Non-lifecycle goals handled directly.
	switch goal {
	case "tree":
		fmt.Printf("%s:%s:%s\n", m.Eff.G, m.Eff.A, m.Eff.V)
		resolver.PrintTree(os.Stdout, nodes)
		return nil
	case "classpath":
		fmt.Println(strings.Join(resolver.Classpath(arts, o.scope), string(os.PathListSeparator)))
		return nil
	}

	// Build effective props: POM props + plugin-management compiler overrides.
	effProps := make(map[string]string, len(m.Eff.Props)+4)
	for k, v := range m.Eff.Props {
		effProps[k] = v
	}
	for k, v := range m.Eff.Build.CompilerProps() {
		if effProps[k] == "" {
			effProps[k] = v
		}
	}

	bc := makeBuildContext(m, arts, loader, effProps)
	lc := buildLifecycle(ctx, loader, m, arts, o, mlog)
	if err := lc.RunUpTo(ctx, lifecycle.Phase(goal), bc); err != nil {
		return err
	}
	return nil
}

func hasLauncher(arts []resolver.Artifact) bool {
	for _, a := range arts {
		if strings.HasPrefix(a.ArtifactID, "junit-platform-console") {
			return true
		}
	}
	return false
}

// jvmArgsFromArgLine splits a surefire argLine into individual JVM args.
// Unresolved ${...} and @{argLine} references (late-binding from jacoco etc.)
// are silently dropped; those projects delegate to mvn.
func jvmArgsFromArgLine(argLine string, props map[string]string) []string {
	if argLine == "" {
		return nil
	}
	resolved := pom.Interpolate(argLine, props)
	var out []string
	for _, tok := range strings.Fields(resolved) {
		if strings.Contains(tok, "${") || strings.Contains(tok, "@{") {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// surefireDefaultIncludes are Maven Surefire's default test class patterns.
var surefireDefaultIncludes = []string{"**/Test*.java", "**/*Test.java", "**/*Tests.java", "**/*TestCase.java"}

// surefirePatterns derives include/exclude class-name regexes from the effective surefire
// config. When no includes are configured the four Surefire defaults apply.
func surefirePatterns(cfg pom.SurefireCfg) (includes, excludes []string) {
	globs := cfg.Includes
	if len(globs) == 0 {
		globs = surefireDefaultIncludes
	}
	for _, g := range globs {
		includes = append(includes, runner.AntGlobToClassRegex(g))
	}
	for _, g := range cfg.Excludes {
		excludes = append(excludes, runner.AntGlobToClassRegex(g))
	}
	return
}
