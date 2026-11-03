// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeReactor lays out a parent POM with n independent modules plus an "app" module that uses a
// class of the "lib" module, so a correct build needs both parallelism and dependency order.
func writeReactor(t *testing.T, root string, n int) {
	t.Helper()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	modules := "<module>lib</module><module>app</module>"
	for i := 1; i <= n; i++ {
		modules += fmt.Sprintf("<module>m%d</module>", i)
	}
	write(filepath.Join(root, "pom.xml"), `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion>
<groupId>par</groupId><artifactId>parent</artifactId><version>1.0</version><packaging>pom</packaging>
<properties><maven.compiler.release>17</maven.compiler.release></properties>
<modules>`+modules+`</modules></project>`)

	child := func(name, extra string) {
		write(filepath.Join(root, name, "pom.xml"), `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion>
<parent><groupId>par</groupId><artifactId>parent</artifactId><version>1.0</version></parent>
<artifactId>`+name+`</artifactId>`+extra+`</project>`)
	}
	child("lib", "")
	write(filepath.Join(root, "lib", "src", "main", "java", "lib", "Lib.java"),
		"package lib; public class Lib { public static int answer() { return 42; } }")
	child("app", `<dependencies><dependency><groupId>par</groupId><artifactId>lib</artifactId><version>1.0</version></dependency></dependencies>`)
	write(filepath.Join(root, "app", "src", "main", "java", "app", "App.java"),
		"package app; public class App { public static int v() { return lib.Lib.answer(); } }")
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("m%d", i)
		child(name, "")
		for j := 1; j <= 8; j++ {
			write(filepath.Join(root, name, "src", "main", "java", name, fmt.Sprintf("C%d.java", j)),
				fmt.Sprintf("package %s; public class C%d { public int v() { return %d; } }", name, j, j))
		}
	}
}

// Regression test: modules used to chdir into their directory, so building several at once
// corrupted each other's relative paths (missing target dirs, "file not found", silently missing
// classes). Every module must come out complete, however the workers interleave.
func TestParallelMultiModuleBuildIsCorrect(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("javac not available")
	}
	const n = 6
	t.Setenv("JV_JOBS", fmt.Sprint(n+2))
	t.Setenv("JV_REPO", t.TempDir())

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	for round := 0; round < 3; round++ {
		root := t.TempDir()
		writeReactor(t, root, n)
		if err := os.Chdir(root); err != nil {
			t.Fatal(err)
		}
		if code := run([]string{"-q", "-o", "compile"}); code != 0 {
			t.Fatalf("round %d: jv compile exited with %d", round, code)
		}
		if err := os.Chdir(orig); err != nil {
			t.Fatal(err)
		}
		must := []string{
			filepath.Join(root, "lib", "target", "classes", "lib", "Lib.class"),
			filepath.Join(root, "app", "target", "classes", "app", "App.class"),
		}
		for i := 1; i <= n; i++ {
			for j := 1; j <= 8; j++ {
				must = append(must, filepath.Join(root, fmt.Sprintf("m%d", i), "target", "classes", fmt.Sprintf("m%d", i), fmt.Sprintf("C%d.class", j)))
			}
		}
		for _, f := range must {
			if _, err := os.Stat(f); err != nil {
				t.Fatalf("round %d: missing %s", round, f)
			}
		}
		if _, err := os.Stat(filepath.Join(orig, "target")); err == nil {
			t.Fatal("a build left a target directory in the process working directory")
		}
	}
}

// package: classes compile in parallel, the remaining phases run module by module.
func TestParallelMultiModulePackage(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("javac not available")
	}
	t.Setenv("JV_JOBS", "4")
	t.Setenv("JV_REPO", t.TempDir())
	orig, _ := os.Getwd()
	defer func() { _ = os.Chdir(orig) }()

	root := t.TempDir()
	writeReactor(t, root, 3)
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"-q", "-o", "package"}); code != 0 {
		t.Fatalf("jv package exited with %d", code)
	}
	for _, mod := range []string{"lib", "app", "m1", "m2", "m3"} {
		jar := filepath.Join(root, mod, "target", mod+"-1.0.jar")
		if _, err := os.Stat(jar); err != nil {
			t.Errorf("missing %s", jar)
		}
	}
}
