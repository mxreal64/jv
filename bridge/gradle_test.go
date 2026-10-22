// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func wrapperName() string {
	if runtime.GOOS == "windows" {
		return "gradlew.bat"
	}
	return "gradlew"
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func TestDetectGradleMarkers(t *testing.T) {
	for _, marker := range []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"} {
		dir := t.TempDir()
		touch(t, filepath.Join(dir, marker))
		if ok, exe := DetectGradle(dir); !ok || exe == "" {
			t.Errorf("DetectGradle with %s = (%v, %q); want a Gradle project and an executable", marker, ok, exe)
		}
	}
}

func TestDetectGradleNone(t *testing.T) {
	if ok, exe := DetectGradle(t.TempDir()); ok {
		t.Errorf("DetectGradle on an empty dir = (true, %q); want false", exe)
	}
}

func TestDetectGradlePrefersWrapperAndWalksUp(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "settings.gradle"))
	touch(t, filepath.Join(root, wrapperName()))
	sub := filepath.Join(root, "modules", "core")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	ok, exe := DetectGradle(sub)
	if want := filepath.Join(root, wrapperName()); !ok || exe != want {
		t.Errorf("DetectGradle(%s) = (%v, %q); want the wrapper %q", sub, ok, exe, want)
	}
}

func TestTranslateMavenArgsTable(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"profile attached", []string{"test", "-Pci"}, []string{"-Pci", "test"}},
		{"profile separate", []string{"-P", "ci", "test"}, []string{"-Pci", "test"}},
		{"property attached", []string{"-Dfoo=bar", "test"}, []string{"-Dfoo=bar", "test"}},
		{"property separate", []string{"-D", "foo=bar", "test"}, []string{"-Dfoo=bar", "test"}},
		{"maven.test.skip", []string{"package", "-Dmaven.test.skip=true"}, []string{"-x", "test", "build"}},
		{"verbose", []string{"-X", "compile"}, []string{"--info", "classes"}},
		{"project with colon prefix", []string{"-pl", ":core", "clean", "test"}, []string{":core:clean", ":core:test"}},
		{"long projects flag", []string{"--projects", "app", "package"}, []string{":app:build"}},
		{"several projects", []string{"-pl", "a,b", "test"}, []string{":a:test", ":b:test"}},
		{"nested project path", []string{"-pl", "services/core", "test"}, []string{":services:core:test"}},
		{"settings file is dropped", []string{"-s", "settings.xml", "compile"}, []string{"classes"}},
		{"unknown goal passes through", []string{"verify"}, []string{"verify"}},
		{"unknown flag passes through", []string{"--stacktrace", "test"}, []string{"--stacktrace", "test"}},
	}
	for _, tt := range tests {
		if got := TranslateMavenArgs(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: TranslateMavenArgs(%v) = %v; want %v", tt.name, tt.in, got, tt.want)
		}
	}
}

// fakeWrapper writes a gradlew that records its arguments (one per line) and exits with rc.
func fakeWrapper(t *testing.T, dir string, rc int) (argsFile string) {
	t.Helper()
	argsFile = filepath.Join(dir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nexit %d\n", argsFile, rc)
	if err := os.WriteFile(filepath.Join(dir, "gradlew"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return argsFile
}

func TestFallbackToGradleUsesWrapperAndPropagatesExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gradlew is a POSIX shell script")
	}
	dir := t.TempDir()
	argsFile := fakeWrapper(t, dir, 7)
	chdir(t, dir)

	rc := FallbackToGradle([]string{"-o", "compile", "-pl", "core"})
	if rc == 127 {
		t.Skip("cannot execute scripts from the temp dir")
	}
	if rc != 7 {
		t.Fatalf("FallbackToGradle exit code = %d; want 7 (the wrapper's)", rc)
	}
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got, want := strings.Fields(string(data)), []string{"--offline", ":core:classes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wrapper received %v; want %v", got, want)
	}
}

func TestFallbackToGradleWithoutProject(t *testing.T) {
	chdir(t, t.TempDir())
	if rc := FallbackToGradle([]string{"compile"}); rc != 127 {
		t.Errorf("FallbackToGradle outside a Gradle project = %d; want 127", rc)
	}
}
