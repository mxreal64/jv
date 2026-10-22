package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
