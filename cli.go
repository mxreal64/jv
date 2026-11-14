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
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mxreal64/jv/bridge"
	"github.com/mxreal64/jv/cmd"
	"github.com/mxreal64/jv/compiler"
	"github.com/mxreal64/jv/config"
	"github.com/mxreal64/jv/lifecycle"
	"github.com/mxreal64/jv/model"
	"github.com/mxreal64/jv/packager"
	"github.com/mxreal64/jv/plugin"
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
	version = "v0.3.0-rc2"
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
explain <dep>   show why a dependency is on the classpath (artifactId, group:artifactId[:version])
audit       check the resolved dependencies against the OSV vulnerability database
            (-Daudit.failOn=none|low|medium|high|critical, -Daudit.json=true; exit 1 on findings)
upgrade     list dependencies that have a newer release (-Dupgrade.all=true includes transitive ones)
init        create a new Maven project here (-DgroupId= -DartifactId= -Dversion= -Dtype=spring-boot)
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
	"explain":   true,
	"audit":     true,
	"upgrade":   true,
	"init":      true,
	"watch":     true,
}

type cliOpts struct {
	goals                          []string
	scope                          string
	query                          string // explain <query>
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
			if a == "explain" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				o.query = args[i]
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

	// jv init — scaffold a project; needs no existing build.
	if len(o.goals) > 0 && o.goals[0] == "init" {
		return runInit(o, root)
	}
	if hasAnalysisGoal(o.goals) {
		if !hasPom {
			errf("jv %s works on Maven projects and needs a pom.xml in this directory", strings.Join(o.goals, " "))
			return 2
		}
		if o.goals[0] == "explain" && o.query == "" {
			errf("usage: jv explain <artifactId | groupId:artifactId[:version]>")
			return 2
		}
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
			return fallbackMaven(args, root)
		}
	}
	if len(o.unknown) > 0 {
		if !hasPom && isGradle {
			return bridge.FallbackToGradle(args)
		}
		logf("flag(s) %s not handled natively; delegating to mvn", strings.Join(o.unknown, " "))
		return fallbackMaven(args, root)
	}

	if _, err := pom.ParsePOM(filepath.Join(root, "pom.xml")); err != nil {
		if isGradle {
			logf("no usable pom.xml; delegating to Gradle")
			return bridge.FallbackToGradle(args)
		}
		logf("no usable pom.xml (%v); delegating to mvn", err)
		return fallbackMaven(args, root)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer compiler.ShutdownWorker()
	compiler.SetVerbose(o.verbose) // FIX #2: surface daemon start/fail in -v mode
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

	repoDir := settings.RepoDir()
	if ap, aerr := filepath.Abs(repoDir); aerr == nil {
		repoDir = ap // modules build concurrently: nothing may depend on the working directory
	}
	loader := resolver.NewLoader(ctx, repoDir, settings)
	if u := os.Getenv("JV_CACHE_URL"); u != "" && !o.offline {
		loader.Dist = resolver.NewDistCache(u, os.Getenv("JV_CACHE_TOKEN"))
		defer loader.FlushDist()
		if resolver.InsecureCacheURL(u) {
			logf("warning: JV_CACHE_URL uses plain http; artifacts are still verified against their SHA-1, but traffic and token are unprotected")
		}
	}
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
			return fallbackMaven(args, root)
		}
	}
	needsBuild := false
	for _, g := range build {
		if g != "clean" && g != "classpath" && g != "tree" && !analysisGoals[g] {
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
			return fallbackMaven(args, root)
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

	// jv watch [goal] — continuous rebuild on source changes.
	if len(build) > 0 && build[0] == "watch" {
		watchGoal := "test"
		if len(build) > 1 {
			watchGoal = build[1]
		}
		// FIX #1: build real per-module compile-classpath map so dirty propagation
		// uses actual dependency edges, not path-prefix heuristics.
		compileCP := make(map[string][]string, len(ordered))
		for _, m := range ordered {
			if m.Classes == "" {
				continue
			}
			arts, _, err := resolveModule(loader, m, false, func(string, ...any) {})
			if err == nil {
				compileCP[m.Classes] = resolver.Classpath(arts, "compile")
			}
		}
		wo := watchOpts{
			goal:      watchGoal,
			cliOpts:   o,
			compileCP: compileCP,
		}
		return runWatch(ctx, wo, loader, ordered)
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
		if (goal == "classpath" || goal == "tree" || analysisGoals[goal]) && len(o.pl) > 0 {
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
		if analysisGoals[goal] {
			if code := runAnalysis(ctx, loader, targets, goal, o); code != 0 {
				return code
			}
			continue
		}
		if err := buildTargets(ctx, loader, targets, goal, o); err != nil {
			if ctx.Err() != nil {
				errf("interrupted")
			} else {
				errf("%v", err)
			}
			return 1
		}
	}
	logf("done in %v", time.Since(start).Round(time.Millisecond))
	return 0
}

func resolveModule(loader *resolver.Loader, m *resolver.Module, forTree bool, mlog func(string, ...any)) ([]resolver.Artifact, []*resolver.Node, error) {
	lock := filepath.Join(m.Dir, ".jv", "lock.json")
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

	abs := func(rel string) string { return filepath.Join(m.Dir, filepath.FromSlash(rel)) }
	return lifecycle.BuildContext{
		Roots:                 &lifecycle.SourceRoots{},
		Group:                 m.Eff.G,
		Artifact:              m.Eff.A,
		Version:               m.Eff.V,
		Packaging:             m.Eff.Packaging,
		Dir:                   m.Dir,
		SourceDirs:            []string{abs("src/main/java")},
		TestSourceDirs:        []string{abs("src/test/java")},
		ResourceDirs:          []string{abs("src/main/resources")},
		TestResourceDirs:      []string{abs("src/test/resources")},
		ResourceFiltering:     filtering,
		TestResourceFiltering: testFiltering,
		ClassesDir:            abs("target/classes"),
		TestClassesDir:        abs("target/test-classes"),
		GeneratedDir:          abs("target/generated-sources/annotations"),
		TargetDir:             abs("target"),
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
	jvTest model.TestConfig,
	jvGenerators []model.GeneratorDecl,
	jvPlugins []lifecycle.Executor,
) *lifecycle.Lifecycle {
	lc := lifecycle.New()

	// [[generator]] entries of jv.toml: external commands that write sources, which are then compiled.
	for i, gd := range jvGenerators {
		id, phase := generatorID(gd, i), generatorPhase(gd)
		g := codegen.Generator{ID: id, Phase: phase, Command: gd.Command, OutputDir: gd.Output, Inputs: gd.Inputs}
		lc.Add(lifecycle.FuncExecutor{PhaseVal: lifecycle.Phase(phase), IDVal: "jv.toml:" + id, Fn: func(ctx context.Context, bc lifecycle.BuildContext) error {
			return codegen.Run(ctx, g, &bc)
		}})
	}
	// [[plugin]] entries of jv.toml: installed external executables speaking jv's JSON protocol.
	for _, e := range jvPlugins {
		lc.Add(e)
	}

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
			if err := codegen.Run(ctx, g, &bc); err != nil {
				return err // a failed generator must stop the build, not leave its classes missing
			}
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
		compiled, err := compiler.CompileDirs(ctx, bc.AllSourceDirs(), bc.ClassesDir, bc.CompileCP, opts)
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
		testCp := append([]string{bc.TestClassesDir, bc.ClassesDir}, bc.TestCP...)
		tc, err := compiler.CompileDirs(ctx, bc.AllTestSourceDirs(), bc.TestClassesDir, testCp, opts)
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
		sfCfg := applyJVTestConfig(m.Eff.Build.SurefireConfig(), jvTest)
		runCfg := runner.Config{
			Dir:            bc.Dir,
			Scan:           bc.TestClassesDir,
			Reports:        filepath.Join(bc.TargetDir, "surefire-reports"),
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
		if m != nil && m.POMFile != "" {
			metaMavenDir := filepath.Join(bc.ClassesDir, "META-INF", "maven", bc.Group, bc.Artifact)
			if err := os.MkdirAll(metaMavenDir, 0o755); err == nil {
				if pomBytes, err := os.ReadFile(m.POMFile); err == nil {
					_ = os.WriteFile(filepath.Join(metaMavenDir, "pom.xml"), pomBytes, 0o644)
				}
				propsContent := fmt.Sprintf("#Generated by jv\nversion=%s\ngroupId=%s\nartifactId=%s\n", bc.Version, bc.Group, bc.Artifact)
				_ = os.WriteFile(filepath.Join(metaMavenDir, "pom.properties"), []byte(propsContent), 0o644)
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

// preparedModule is a module whose dependencies are resolved and whose lifecycle is ready to run.
type preparedModule struct {
	lc *lifecycle.Lifecycle
	bc lifecycle.BuildContext
}

func buildModule(ctx context.Context, loader *resolver.Loader, m *resolver.Module, goal string, o *cliOpts, multi bool) error {
	pm, err := prepareModule(ctx, loader, m, goal, o, multi)
	if err != nil || pm == nil {
		return err
	}
	return pm.lc.RunUpTo(ctx, lifecycle.Phase(goal), pm.bc)
}

// prepareModule resolves the module's dependencies and builds its lifecycle. Goals that need no
// lifecycle (pom modules, tree, classpath) are completed here and yield a nil result. It never
// changes the working directory, so any number of modules can be prepared and built concurrently.
func prepareModule(ctx context.Context, loader *resolver.Loader, m *resolver.Module, goal string, o *cliOpts, multi bool) (*preparedModule, error) {
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
			return nil, packager.Install(loader.Repo, m.Eff.G, m.Eff.A, m.Eff.V, "", m.POMFile)
		}
		return nil, nil
	}
	arts, nodes, err := resolveModule(loader, m, goal == "tree", mlog)
	if err != nil {
		return nil, err
	}
	// Non-lifecycle goals handled directly.
	switch goal {
	case "tree":
		fmt.Printf("%s:%s:%s\n", m.Eff.G, m.Eff.A, m.Eff.V)
		resolver.PrintTree(os.Stdout, nodes)
		return nil, nil
	case "classpath":
		fmt.Println(strings.Join(resolver.Classpath(arts, o.scope), string(os.PathListSeparator)))
		return nil, nil
	}

	// jv.toml: jv's own per-module settings, layered over the POM (command-line -D still wins).
	jt, err := model.Load(m.Dir)
	if err != nil {
		return nil, err
	}
	for _, w := range jt.Warnings {
		mlog("warning: jv.toml: %s", w)
	}
	if err := validateGenerators(jt.Generators); err != nil {
		return nil, fmt.Errorf("jv.toml: %w", err)
	}
	jvPlugins, err := preparePlugins(ctx, loader, jt.Plugins)
	if err != nil {
		return nil, fmt.Errorf("jv.toml: %w", err)
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

	for k, v := range map[string]string{
		"maven.compiler.release":       jt.Build.Release,
		"maven.compiler.source":        jt.Build.Source,
		"project.build.sourceEncoding": jt.Build.Encoding,
	} {
		if _, fromCLI := o.props[k]; v != "" && !fromCLI {
			effProps[k] = v
		}
	}

	bc := makeBuildContext(m, arts, loader, effProps)
	return &preparedModule{lc: buildLifecycle(ctx, loader, m, arts, o, mlog, jt.Test, jt.Generators, jvPlugins), bc: bc}, nil
}

// buildTargets builds the selected modules for one goal. Modules whose dependencies are done
// compile in parallel (JV_JOBS workers, default one per CPU). The phases after test-compile (tests,
// packaging, install) then run one module at a time in dependency order, as Maven does, so tests
// of different modules never run concurrently.
func buildTargets(ctx context.Context, loader *resolver.Loader, targets []*resolver.Module, goal string, o *cliOpts) error {
	multi := len(targets) > 1
	idx := lifecycle.IndexOf(lifecycle.Phase(goal))
	if !multi || idx < 0 {
		for _, m := range targets {
			if err := buildModule(ctx, loader, m, goal, o, multi); err != nil {
				return fmt.Errorf("%s: %w", m.Eff.A, err)
			}
		}
		return nil
	}

	const split = lifecycle.Phase("test-compile")
	first := lifecycle.Phase(goal)
	if idx > lifecycle.IndexOf(split) {
		first = split
	}
	var prepared sync.Map // *resolver.Module -> *preparedModule
	err := lifecycle.RunGraph(ctx, targets, lifecycle.DefaultWorkers(), func(c context.Context, m *resolver.Module) error {
		pm, err := prepareModule(c, loader, m, goal, o, true)
		if err != nil {
			return fmt.Errorf("%s: %w", m.Eff.A, err)
		}
		if pm == nil {
			return nil
		}
		if err := pm.lc.RunUpTo(c, first, pm.bc); err != nil {
			return fmt.Errorf("%s: %w", m.Eff.A, err)
		}
		prepared.Store(m, pm)
		return nil
	})
	if err != nil || first == lifecycle.Phase(goal) {
		return err
	}
	for _, m := range targets { // targets are in dependency order
		v, ok := prepared.Load(m)
		if !ok {
			continue
		}
		pm := v.(*preparedModule)
		if err := pm.lc.RunAfter(ctx, split, lifecycle.Phase(goal), pm.bc); err != nil {
			return fmt.Errorf("%s: %w", m.Eff.A, err)
		}
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

// preparePlugins validates the [[plugin]] entries of jv.toml and finds their installed executables. A
// plugin that is not installed fails the build up front; jv does not download plugins.
func preparePlugins(ctx context.Context, loader *resolver.Loader, decls []model.PluginDecl) ([]lifecycle.Executor, error) {
	seen := map[string]bool{}
	var out []lifecycle.Executor
	for _, pd := range decls {
		parts := strings.Split(pd.ID, ":")
		switch {
		case len(parts) != 2 || parts[0] == "" || parts[1] == "":
			return nil, fmt.Errorf("[[plugin]] id %q must look like groupId:artifactId", pd.ID)
		case pd.Version == "":
			return nil, fmt.Errorf("[[plugin]] %s needs a version", pd.ID)
		case lifecycle.IndexOf(lifecycle.Phase(pd.Phase)) < 0:
			return nil, fmt.Errorf("[[plugin]] %s: phase %q is not a lifecycle phase (e.g. generate-sources, compile, package)", pd.ID, pd.Phase)
		case seen[pd.ID+"@"+pd.Phase]:
			return nil, fmt.Errorf("[[plugin]] %s is declared twice for phase %s", pd.ID, pd.Phase)
		}
		seen[pd.ID+"@"+pd.Phase] = true
		desc, err := plugin.Resolve(ctx, pd.ID, pd.Version, loader)
		if err != nil {
			return nil, err
		}
		out = append(out, &plugin.PluginExecutor{
			Descriptor: desc,
			Req:        plugin.Request{Phase: pd.Phase, ID: pd.ID, Config: pd.Config},
			Inputs:     pd.Inputs,
		})
	}
	return out, nil
}

func generatorID(g model.GeneratorDecl, i int) string {
	if g.ID != "" {
		return g.ID
	}
	return fmt.Sprintf("generator-%d", i+1)
}

func generatorPhase(g model.GeneratorDecl) string {
	if g.Phase != "" {
		return g.Phase
	}
	return "generate-sources"
}

var generatorIDRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validateGenerators checks [[generator]] entries up front so a typo fails the build with a clear
// message instead of producing a project that compiles without its generated sources.
func validateGenerators(gens []model.GeneratorDecl) error {
	seen := map[string]bool{}
	for i, g := range gens {
		id := generatorID(g, i)
		switch {
		case !generatorIDRE.MatchString(id):
			return fmt.Errorf("[[generator]] id %q may only contain letters, digits, '.', '_' and '-'", id)
		case seen[id]:
			return fmt.Errorf("[[generator]] id %q is used twice", id)
		case len(g.Command) == 0:
			return fmt.Errorf("[[generator]] %q needs a command", id)
		}
		if p := generatorPhase(g); p != "generate-sources" && p != "generate-test-sources" {
			return fmt.Errorf("[[generator]] %q: phase must be generate-sources or generate-test-sources, got %q", id, p)
		}
		seen[id] = true
	}
	return nil
}

// applyJVTestConfig layers the [test] table of jv.toml over the surefire configuration of the POM:
// every setting that jv.toml gives replaces the POM's.
func applyJVTestConfig(cfg pom.SurefireCfg, t model.TestConfig) pom.SurefireCfg {
	if len(t.Includes) > 0 {
		cfg.Includes = t.Includes
	}
	if len(t.Excludes) > 0 {
		cfg.Excludes = t.Excludes
	}
	if t.Groups != "" {
		cfg.Groups = t.Groups
	}
	if t.ExcludedGroups != "" {
		cfg.ExcludedGroups = t.ExcludedGroups
	}
	if t.ArgLine != "" {
		cfg.ArgLine = t.ArgLine
	}
	return cfg
}

// fallbackMaven hands the build to mvn. jv.toml is jv's own file, so mvn cannot see it; say so rather
// than silently dropping its settings.
func fallbackMaven(args []string, root string) int {
	if _, err := os.Stat(filepath.Join(root, "jv.toml")); err == nil {
		logf("warning: jv.toml settings are ignored because this build is delegated to mvn")
	}
	return bridge.FallbackToMaven(args)
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
