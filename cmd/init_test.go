// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitProjectCreatesAProject(t *testing.T) {
	dir := t.TempDir()
	if err := InitProject(dir, "org.acme", "demo", "1.2", ""); err != nil {
		t.Fatal(err)
	}
	pom, err := os.ReadFile(filepath.Join(dir, "pom.xml"))
	if err != nil || !strings.Contains(string(pom), "<artifactId>demo</artifactId>") || !strings.Contains(string(pom), "<version>1.2</version>") {
		t.Fatalf("pom.xml: %v\n%s", err, pom)
	}
	for _, f := range []string{"src/main/java/org/acme/App.java", "src/test/java/org/acme/AppTest.java", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("missing %s", f)
		}
	}
}

// init must never destroy an existing build.
func TestInitProjectRefusesToOverwrite(t *testing.T) {
	for _, existing := range []string{"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, existing), []byte("keep me"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := InitProject(dir, "g", "a", "1", "")
		if !errors.Is(err, ErrProjectExists) {
			t.Errorf("%s: want ErrProjectExists, got %v", existing, err)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, existing)); string(b) != "keep me" {
			t.Errorf("%s was modified", existing)
		}
		if _, err := os.Stat(filepath.Join(dir, "src")); err == nil {
			t.Errorf("%s: nothing may be written when init refuses", existing)
		}
	}
}
