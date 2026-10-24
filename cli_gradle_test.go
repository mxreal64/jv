// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mxreal64/jv/gradle"
)

func TestGradleExtraArgs(t *testing.T) {
	got := gradleExtraArgs([]string{"-q", "-Pfoo=1", "-Dorg.x=2", "-DskipTests", "-Dtest=Foo", "-Dmaven.test.skip=true", "compile", "-P"})
	want := []string{"-Pfoo=1", "-Dorg.x=2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestFindGradleRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "app", "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findGradleRoot(sub); got != "" {
		t.Fatalf("no gradle files yet, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "settings.gradle.kts"), []byte("rootProject.name=\"x\""), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findGradleRoot(filepath.Join(root, "app")); got != root {
		t.Fatalf("got %q want %q", got, root)
	}
	solo := t.TempDir()
	if err := os.WriteFile(filepath.Join(solo, "build.gradle"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findGradleRoot(solo); got != solo {
		t.Fatalf("single-project build: got %q", got)
	}
}

func TestManifestAttrs(t *testing.T) {
	got := manifestAttrs(map[string]string{"Implementation-Title": "x", "Main-Class": "a.B", "Manifest-Version": "1.0", "Automatic-Module-Name": "m"})
	want := [][2]string{{"Main-Class", "a.B"}, {"Automatic-Module-Name", "m"}, {"Implementation-Title", "x"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestGradleGoalsAreNative(t *testing.T) {
	for _, g := range []string{"clean", "compile", "test", "package", "classpath"} {
		if !gradleGoals[g] {
			t.Errorf("%s should be served natively", g)
		}
	}
	if gradleGoals["install"] || gradleGoals["tree"] {
		t.Error("install and tree must go to Gradle")
	}
}

func TestSafeToClean(t *testing.T) {
	root := filepath.FromSlash("/w")
	proj := func(buildDir string) *gradle.Project {
		return &gradle.Project{
			Dir:      filepath.FromSlash("/w/app"),
			BuildDir: filepath.FromSlash(buildDir),
			MainJava: []string{filepath.FromSlash("/w/app/src/main/java")},
		}
	}
	// In order: normal, out-of-tree build dir, empty, project dir, build root, filesystem root, sources.
	tests := []struct {
		buildDir string
		want     bool
	}{
		{"/w/app/build", true},
		{"/w/build/app", true},
		{"", false},
		{"/w/app", false},
		{"/w", false},
		{"/", false},
		{"/w/app/src", false},
	}
	for _, tt := range tests {
		if got := safeToClean(proj(tt.buildDir), root); got != tt.want {
			t.Errorf("safeToClean(buildDir=%q) = %v; want %v", tt.buildDir, got, tt.want)
		}
	}
}

// gradleDir makes a directory that looks like a Gradle build (the model is supplied by the cache).
func gradleDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "build.gradle"), []byte("plugins { id 'java' }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func javaProject(projectDir, path, name string) gradle.Project {
	p := gradle.Project{Path: path, Dir: projectDir, Name: name, Java: true}
	p.BuildDir = filepath.Join(projectDir, "build")
	p.Plugins = []string{"org.gradle.api.plugins.JavaPlugin"}
	p.MainJava = []string{filepath.Join(projectDir, "src", "main", "java")}
	p.MainClasses = []string{filepath.Join(p.BuildDir, "classes", "java", "main")}
	p.MainResOut = filepath.Join(p.BuildDir, "resources", "main")
	p.Jar = filepath.Join(p.BuildDir, "libs", name+".jar")
	p.CompileDeps = []string{"compileJava"}
	return p
}

// cacheModel stores m as the cached model of the build in dir, as a first run would have.
func cacheModel(t *testing.T, dir string, m *gradle.Model) {
	t.Helper()
	key, err := gradle.CacheKey(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := gradle.SaveCache(dir, key, m); err != nil {
		t.Fatal(err)
	}
}

func touchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestRunGradleNativeDelegation(t *testing.T) {
	dir := gradleDir(t)
	missing := filepath.Join(dir, "no-such-gradle") // never started for these cases, or fails to start
	tests := []struct {
		name, oracle string
		args         []string
	}{
		{"oracle disabled", "0", []string{"compile"}},
		{"goal gradle must run itself", "", []string{"install"}},
		{"flag jv does not know", "", []string{"--fail-never", "compile"}},
		{"maven profile", "", []string{"-Pci", "compile"}},
		{"gradle cannot be started", "", []string{"compile"}},
	}
	for _, tt := range tests {
		t.Setenv("JV_GRADLE_ORACLE", tt.oracle)
		if code, handled := runGradleNative(parseArgs(tt.args), tt.args, dir, missing); handled || code != 0 {
			t.Errorf("%s: runGradleNative = (%d, %v); want (0, false) so the bridge runs Gradle", tt.name, code, handled)
		}
	}
}

func TestRunGradleNativeCleanFromCachedModel(t *testing.T) {
	t.Setenv("JV_GRADLE_ORACLE", "")
	dir := gradleDir(t)
	p := javaProject(dir, ":", "p")
	touchFile(t, filepath.Join(p.BuildDir, "out.txt"))
	touchFile(t, filepath.Join(p.MainJava[0], "App.java"))
	cacheModel(t, dir, &gradle.Model{Root: dir, Projects: []gradle.Project{p}})

	args := []string{"-q", "clean"}
	// the gradle command does not exist: a native clean must not need it
	if code, handled := runGradleNative(parseArgs(args), args, dir, filepath.Join(dir, "no-such-gradle")); !handled || code != 0 {
		t.Fatalf("runGradleNative(clean) = (%d, %v); want (0, true)", code, handled)
	}
	if exists(p.BuildDir) {
		t.Error("clean did not remove the build directory")
	}
	if !exists(filepath.Join(p.MainJava[0], "App.java")) {
		t.Error("clean removed a source file")
	}
}

func TestRunGradleNativeCleanRefusesUnsafeBuildDir(t *testing.T) {
	t.Setenv("JV_GRADLE_ORACLE", "")
	dir := gradleDir(t)
	p := javaProject(dir, ":", "p")
	p.BuildDir = dir // buildDir = projectDir
	src := filepath.Join(p.MainJava[0], "App.java")
	touchFile(t, src)
	cacheModel(t, dir, &gradle.Model{Root: dir, Projects: []gradle.Project{p}})

	args := []string{"-q", "clean"}
	if code, handled := runGradleNative(parseArgs(args), args, dir, filepath.Join(dir, "no-such-gradle")); !handled || code != 1 {
		t.Fatalf("runGradleNative(clean) = (%d, %v); want (1, true)", code, handled)
	}
	if !exists(src) || !exists(filepath.Join(dir, "build.gradle")) {
		t.Fatal("the project was deleted")
	}
}

func TestRunGradleNativeDelegatesOnProjectCycle(t *testing.T) {
	t.Setenv("JV_GRADLE_ORACLE", "")
	dir := gradleDir(t)
	a := javaProject(filepath.Join(dir, "a"), ":a", "a")
	b := javaProject(filepath.Join(dir, "b"), ":b", "b")
	a.TestRuntime = []string{b.Jar}
	b.Compile = []string{a.Jar}
	marker := filepath.Join(a.BuildDir, "out.txt")
	touchFile(t, marker)
	cacheModel(t, dir, &gradle.Model{Root: dir, Projects: []gradle.Project{a, b}})

	args := []string{"-q", "clean"}
	if code, handled := runGradleNative(parseArgs(args), args, dir, filepath.Join(dir, "no-such-gradle")); handled || code != 0 {
		t.Fatalf("runGradleNative = (%d, %v); want (0, false): a cycle must fall back to gradle", code, handled)
	}
	if !exists(marker) {
		t.Error("nothing should have been cleaned when delegating")
	}
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	f()
	w.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRunGradleNativeClasspathOfOneProject(t *testing.T) {
	t.Setenv("JV_GRADLE_ORACLE", "")
	dir := gradleDir(t)
	core := javaProject(filepath.Join(dir, "core"), ":core", "core")
	app := javaProject(filepath.Join(dir, "app"), ":app", "app")
	app.Compile = []string{core.Jar} // unbuilt: must neither miss the cache nor reach the classpath
	cacheModel(t, dir, &gradle.Model{Root: dir, Projects: []gradle.Project{core, app}})
	missing := filepath.Join(dir, "no-such-gradle")

	args := []string{"-q", "-pl", "app", "classpath", "compile"}
	var code int
	var handled bool
	out := captureStdout(t, func() { code, handled = runGradleNative(parseArgs(args), args, dir, missing) })
	if !handled || code != 0 {
		t.Fatalf("runGradleNative(-pl app classpath) = (%d, %v); want (0, true)", code, handled)
	}
	if !strings.Contains(out, core.MainClasses[0]) || strings.Contains(out, "core.jar") {
		t.Errorf("classpath = %q; want core's class directory in place of its jar", strings.TrimSpace(out))
	}

	args = []string{"-q", "classpath"} // two Java projects and no -pl: ambiguous
	if code, handled := runGradleNative(parseArgs(args), args, dir, missing); !handled || code != 1 {
		t.Errorf("runGradleNative(classpath) at the root = (%d, %v); want (1, true)", code, handled)
	}
}
