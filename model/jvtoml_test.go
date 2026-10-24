// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJVTomlLoad(t *testing.T) {
	dir := t.TempDir()

	// Missing file -> returns zero value, nil error
	empty, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error for missing jv.toml: %v", err)
	}
	if empty.Build.Release != "" {
		t.Fatalf("expected empty config")
	}

	content := `
[build]
release = "21"
encoding = "UTF-8"

[test]
argLine = "-Xmx1024m"
`
	if err := os.WriteFile(filepath.Join(dir, "jv.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if loaded.Build.Release != "21" {
		t.Fatalf("expected release 21, got %q", loaded.Build.Release)
	}
	if loaded.Build.Encoding != "UTF-8" {
		t.Fatalf("expected UTF-8, got %q", loaded.Build.Encoding)
	}
	if loaded.Test.ArgLine != "-Xmx1024m" {
		t.Fatalf("expected argLine, got %q", loaded.Test.ArgLine)
	}
}

func TestWorkspaceLoad(t *testing.T) {
	dir := t.TempDir()

	wsNil, err := LoadWorkspace(dir)
	if err != nil || wsNil != nil {
		t.Fatalf("expected nil for missing workspace")
	}

	content := `modules = ["core", "app", "lib/util"]`
	if err := os.WriteFile(filepath.Join(dir, "jv-workspace.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := LoadWorkspace(dir)
	if err != nil {
		t.Fatalf("LoadWorkspace failed: %v", err)
	}
	if len(ws.Modules) != 3 || ws.Modules[0] != "core" || ws.Modules[2] != "lib/util" {
		t.Fatalf("unexpected modules: %v", ws.Modules)
	}
}
