package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jv-build/jv/bridge"
	"github.com/jv-build/jv/compiler"
	"github.com/jv-build/jv/config"
	"github.com/jv-build/jv/packager"
	"github.com/jv-build/jv/pom"
	"github.com/jv-build/jv/resolver"
	"github.com/jv-build/jv/runner"
)

var (
	version = "dev"
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

	hasPom := false
	if _, err := os.Stat(filepath.Join(root, "pom.xml")); err == nil {
		hasPom = true
	}

	isGradle, gradlew := bridge.DetectGradle(root)

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
	var build []string
	for _, g := range o.goals {
		if g == "clean" && len(o.pl) == 0 {
			for _, d := range dirs {
				_ = os.RemoveAll(filepath.Join(d, "target"))
				_ = os.RemoveAll(filepath.Join(d, ".jv"))
			}
			logf("cleaned %d module(s)", len(dirs))
		} else {
			build = append(build, g) // "clean" with -pl is handled per selected module below
		}
	}
	if len(build) == 0 {
		return 0
	}

	loader := resolver.NewLoader(ctx, settings.RepoDir(), settings)
	loader.Offline, loader.Verbose, loader.ProfileIDs, loader.UserProps = o.offline, o.verbose, o.profiles, o.props
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
		if goal == "clean" { // only reached with -pl: clean just the selected modules
			sel, err := resolver.SelectOnly(ordered, root, o.pl)
			if err != nil {
				errf("%v", err)
				return 1
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
	_ = os.Chdir(root)
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
			return packager.Install(loader.Repo, m.Eff.G, m.Eff.A, m.Eff.V, "", filepath.Join(m.Dir, "pom.xml"))
		}
		return nil
	}
	arts, nodes, err := resolveModule(loader, m, goal == "tree", mlog)
	if err != nil {
		return err
	}
	switch goal {
	case "tree":
		fmt.Printf("%s:%s:%s\n", m.Eff.G, m.Eff.A, m.Eff.V)
		resolver.PrintTree(os.Stdout, nodes)
		return nil
	case "classpath":
		fmt.Println(strings.Join(resolver.Classpath(arts, o.scope), string(os.PathListSeparator)))
		return nil
	}

	opts := compiler.OptionsFrom(m.Eff.Props)
	compiled, err := compiler.Compile(ctx, "src/main/java", "target/classes", resolver.Classpath(arts, "compile"), opts)
	if err != nil {
		return fmt.Errorf("compilation failed: %w", err)
	}
	if err := compiler.CopyResources("src/main/resources", "target/classes"); err != nil {
		return fmt.Errorf("copying resources: %w", err)
	}
	if compiled {
		mlog("compiled main sources")
	} else {
		mlog("main classes up to date")
	}
	if goal == "compile" {
		return nil
	}

	if !o.skipTestCompilation {
		testCp := append([]string{"target/test-classes", "target/classes"}, resolver.Classpath(arts, "test")...)
		tc, err := compiler.Compile(ctx, "src/test/java", "target/test-classes", testCp, opts)
		if err != nil {
			return fmt.Errorf("test compilation failed: %w", err)
		}
		if err := compiler.CopyResources("src/test/resources", "target/test-classes"); err != nil {
			return fmt.Errorf("copying test resources: %w", err)
		}
		if tc {
			mlog("compiled test sources")
		}
		if !o.skipTests {
			if st, err := os.Stat("target/test-classes"); err == nil && st.IsDir() {
				launcher := ""
				if !hasLauncher(arts) {
					ver := os.Getenv("JV_JUNIT_VERSION")
					if ver == "" {
						ver = defaultJUnit
					}
					if launcher, err = loader.FetchJar("org.junit.platform", "junit-platform-console-standalone", ver); err != nil {
						return fmt.Errorf("fetching JUnit launcher: %w", err)
					}
				}
				if err := runner.RunTests(ctx, launcher, testCp, o.props["test"]); err != nil {
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

	jar := filepath.Join("target", m.Eff.A+"-"+m.Eff.V+".jar")
	var attrs [][2]string
	for _, k := range []string{"jv.mainClass", "exec.mainClass", "mainClass"} {
		if mc := m.Eff.Props[k]; mc != "" {
			attrs = append(attrs, [2]string{"Main-Class", mc})
			break
		}
	}
	if err := packager.Jar("target/classes", jar, attrs); err != nil {
		return fmt.Errorf("packaging: %w", err)
	}
	mlog("built %s", jar)
	if goal == "install" {
		if err := packager.Install(loader.Repo, m.Eff.G, m.Eff.A, m.Eff.V, jar, filepath.Join(m.Dir, "pom.xml")); err != nil {
			return fmt.Errorf("install: %w", err)
		}
		mlog("installed %s:%s:%s into %s", m.Eff.G, m.Eff.A, m.Eff.V, loader.Repo)
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
