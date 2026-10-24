// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package gradle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sampleModel = `{"root":"/w","projects":[
 {"path":":","dir":"/w","name":"w","plugins":[],"java":false},
 {"path":":core","dir":"/w/core","name":"core","java":true,"buildDir":"/w/core/build",
  "plugins":["org.gradle.api.plugins.JavaLibraryPlugin_Decorated","org.gradle.api.plugins.BasePlugin"],
  "mainJava":["/w/core/src/main/java"],"mainRes":["/w/core/src/main/resources"],
  "testJava":["/w/core/src/test/java"],"testRes":[],
  "mainClasses":["/w/core/build/classes/java/main"],"testClasses":["/w/core/build/classes/java/test"],
  "mainResOut":"/w/core/build/resources/main","testResOut":"/w/core/build/resources/test",
  "compile":["/w/core/x.jar"],"runtime":["/w/core/x.jar"],"testCompile":[],"testRuntime":[],
  "compileDeps":["compileJava"],"testEnabled":true,"jar":"/w/core/build/libs/core-1.0.jar","manifest":{"Main-Class":"c.Main"}},
 {"path":":app","dir":"/w/app","name":"app","java":true,"buildDir":"/w/app/build",
  "plugins":["org.gradle.api.plugins.JavaPlugin"],
  "mainJava":["/w/app/src/main/java"],"mainClasses":["/w/app/build/classes/java/main"],
  "testClasses":["/w/app/build/classes/java/test"],
  "compile":["/w/core/build/classes/java/main"],
  "runtime":["/w/core/build/libs/core-1.0.jar","/w/app/y.jar"],"testCompile":[],"testRuntime":[],
  "jar":"/w/app/build/libs/app-1.0.jar","compileDeps":["compileJava","jar"]}
]}`

func load(t *testing.T) *Model {
	t.Helper()
	var m Model
	if err := json.Unmarshal([]byte(sampleModel), &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

func TestOrderSelectSubst(t *testing.T) {
	m := load(t)
	ordered, err := m.Order()
	if err != nil || len(ordered) != 2 || ordered[0].Path != ":core" || ordered[1].Path != ":app" {
		t.Fatalf("order: %v %v", ordered, err)
	}
	for _, name := range []string{":app", "app", "app"} {
		sel, err := m.Select(ordered, []string{name})
		if err != nil || len(sel) != 2 || sel[0].Path != ":core" {
			t.Fatalf("select %q pulls in core: %v %v", name, sel, err)
		}
	}
	if sel, err := m.Select(ordered, []string{"core"}); err != nil || len(sel) != 1 {
		t.Fatalf("select core: %v %v", sel, err)
	}
	if _, err := m.Select(ordered, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "no Java project") {
		t.Fatalf("unknown project: %v", err)
	}
	got := m.Subst(ordered[1].Runtime)
	want := []string{"/w/core/build/classes/java/main", "/w/core/build/resources/main", "/w/app/y.jar"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("subst: %v", got)
	}
}

func TestUnsupported(t *testing.T) {
	m := load(t)
	if u := m.Unsupported(); len(u) != 0 {
		t.Fatalf("plain java build must be supported, got %v", u)
	}
	m.Projects[1].Plugins = append(m.Projects[1].Plugins, "org.jetbrains.kotlin.gradle.plugin.KotlinPluginWrapper")
	m.Projects[1].Processors = []string{"/p/lombok.jar"}
	m.Projects[2].CompileDeps = []string{"compileJava", "generateProto"}
	m.Projects[2].TestFramework = "org.gradle.api.tasks.testing.testng.TestNGOptions"
	u := strings.Join(m.Unsupported(), "\n")
	for _, w := range []string{"KotlinPluginWrapper", "annotation processors", "generateProto", "TestNG"} {
		if !strings.Contains(u, w) {
			t.Errorf("missing %q in:\n%s", w, u)
		}
	}
	m2 := load(t)
	m2.Projects[1].Plugins = []string{"org.gradle.api.plugins.WarPlugin"}
	if u := m2.Unsupported(); len(u) != 1 || !strings.Contains(u[0], "WarPlugin") {
		t.Fatalf("war plugin: %v", u)
	}
}

func TestCacheKeyAndRoundTrip(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("settings.gradle", "include 'a'")
	write("a/build.gradle", "plugins { id 'java' }")
	write("a/build/ignored.gradle", "x") // build output is never an input
	k1, err := CacheKey(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	write("a/build/ignored.gradle", "changed")
	if k2, _ := CacheKey(root, nil); k2 != k1 {
		t.Fatal("files under build/ must not affect the key")
	}
	write("a/build.gradle", "plugins { id 'java' }\ndependencies { }")
	k3, _ := CacheKey(root, nil)
	if k3 == k1 {
		t.Fatal("editing a build script must change the key")
	}
	if k4, _ := CacheKey(root, []string{"-Pfoo=1"}); k4 == k3 {
		t.Fatal("-P arguments must change the key")
	}

	m := load(t)
	for i := range m.Projects { // the sample's jars do not exist on disk
		m.Projects[i].Compile, m.Projects[i].Runtime = nil, nil
		m.Projects[i].TestCompile, m.Projects[i].TestRuntime = nil, nil
	}
	if err := SaveCache(root, k3, m); err != nil {
		t.Fatal(err)
	}
	if got, ok := LoadCache(root, k3); !ok || len(got.Projects) != 3 {
		t.Fatalf("round trip: %v %v", got, ok)
	}
	if _, ok := LoadCache(root, k1); ok {
		t.Fatal("a stale key must miss")
	}
	// A model that points at a jar which no longer exists must be refreshed.
	gone := filepath.Join(root, "gone.jar")
	m.Projects[1].Compile = []string{gone}
	_ = SaveCache(root, k3, m)
	if _, ok := LoadCache(root, k3); ok {
		t.Fatal("a missing jar must invalidate the cache")
	}
}

func TestOnlyDoesNotPullInDependencies(t *testing.T) {
	m := load(t)
	ordered, err := m.Order()
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Only(ordered, []string{"app"})
	if err != nil || len(got) != 1 || got[0].Path != ":app" {
		t.Fatalf("Only(app) = %v, %v; want just :app (Select would add :core)", got, err)
	}
	if got, err := m.Only(ordered, []string{":core", "app"}); err != nil || len(got) != 2 {
		t.Fatalf("Only(:core, app) = %v, %v", got, err)
	}
	if _, err := m.Only(ordered, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "no Java project") {
		t.Fatalf("unknown project: %v", err)
	}
}

func TestOrderReportsCycles(t *testing.T) {
	m := load(t)
	// core's test classpath uses app, app uses core: a project-level cycle.
	m.Projects[1].TestRuntime = []string{"/w/app/build/libs/app-1.0.jar"}
	if _, err := m.Order(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want a cycle error, got %v", err)
	}
}

func TestLoadCacheIgnoresUnbuiltSiblingJars(t *testing.T) {
	root := t.TempDir()
	m := load(t)
	for i := range m.Projects { // the sample's third-party jars do not exist on disk
		m.Projects[i].Compile, m.Projects[i].TestCompile, m.Projects[i].TestRuntime = nil, nil, nil
	}
	m.Projects[1].Runtime = nil
	// :app's runtime classpath names :core's jar, which exists only after a package
	m.Projects[2].Runtime = []string{"/w/core/build/libs/core-1.0.jar"}
	if err := SaveCache(root, "k", m); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadCache(root, "k"); !ok {
		t.Fatal("an unbuilt sibling jar must not invalidate the cache (Gradle would be queried on every run)")
	}
	m.Projects[2].Runtime = append(m.Projects[2].Runtime, "/w/app/missing-third-party.jar")
	if err := SaveCache(root, "k", m); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadCache(root, "k"); ok {
		t.Fatal("a missing third-party jar must still invalidate the cache")
	}
}
