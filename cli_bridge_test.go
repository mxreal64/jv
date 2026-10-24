// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// bridgeProject makes dir a Gradle build whose gradlew records its arguments, and switches the
// native model path off so that jv hands the build to the bridge.
func bridgeProject(t *testing.T, dir string) (argsFile string) {
	t.Helper()
	t.Setenv("JV_GRADLE_ORACLE", "0")
	argsFile = filepath.Join(dir, "args.txt")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nexit 0\n", argsFile)
	if err := os.WriteFile(filepath.Join(dir, "gradlew"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.gradle"), []byte("plugins { id 'java' }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return argsFile
}

func TestRunBridgesGradleOnlyProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gradlew is a POSIX shell script")
	}
	argsFile := bridgeProject(t, t.TempDir())
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"-q", "compile"}, []string{"--quiet", "classes"}},
		{[]string{"-q", "verify"}, []string{"--quiet", "verify"}},
		{[]string{"-q", "-pl", "core", "test"}, []string{"--quiet", ":core:test"}},
		{[]string{"-q", "-pl", "a,b", "test"}, []string{"--quiet", ":a:test", ":b:test"}},
		{[]string{"-q", "clean", "install"}, []string{"--quiet", "clean", "publishToMavenLocal"}},
	}
	for _, tc := range cases {
		_ = os.Remove(argsFile)
		rc := run(tc.args)
		if _, err := os.Stat(argsFile); err != nil && rc == 127 {
			t.Skip("cannot execute scripts from the temp dir")
		}
		if rc != 0 {
			t.Errorf("run(%v) = %d; want 0", tc.args, rc)
			continue
		}
		data, err := os.ReadFile(argsFile)
		if err != nil {
			t.Errorf("run(%v): gradle was not invoked: %v", tc.args, err)
			continue
		}
		if got := strings.Fields(string(data)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("run(%v): gradle received %v; want %v", tc.args, got, tc.want)
		}
	}
}

func TestRunPrefersPomOverGradle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gradlew is a POSIX shell script")
	}
	dir := t.TempDir()
	argsFile := bridgeProject(t, dir)
	pom := "<project><modelVersion>4.0.0</modelVersion><groupId>g</groupId><artifactId>a</artifactId><version>1</version></project>"
	if err := os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(pom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rc := run([]string{"-q", "clean"}); rc != 0 {
		t.Fatalf("run(clean) = %d; want 0", rc)
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Error("a project with a pom.xml was handed to Gradle; want jv to handle it natively")
	}
	if _, err := os.Stat(filepath.Join(dir, "target")); err == nil {
		t.Error("clean did not remove target/")
	}
}
