// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package model

import (
	"os"
	"path/filepath"
	"strings"
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

func TestJVTomlFullDocument(t *testing.T) {
	dir := t.TempDir()
	content := `
# project settings
[build]
release = 21          # bare numbers are fine
source = '17'
encoding = "UTF-8"

[test]
includes = ["**/*Test.java", "**/*IT.java"]
excludes = [
  "**/Slow*.java",   # a comment inside a multi-line array
  "**/Flaky*.java",
]
groups = "fast"
excludedGroups = "slow, flaky"
argLine = "-Xmx1g -Dfile.encoding=\"UTF-8\""

[[generator]]
id = "proto"
phase = "generate-sources"
command = ["protoc", "--java_out=target/gen"]
output = "target/gen"
inputs = ["src/main/proto/*.proto"]

[[generator]]
id = "second"

[[plugin]]
id = "org.acme:thing"
version = "1.2"
phase = "package"
`
	if err := os.WriteFile(filepath.Join(dir, "jv.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Build.Release != "21" || c.Build.Source != "17" || c.Build.Encoding != "UTF-8" {
		t.Errorf("build = %+v", c.Build)
	}
	if got := strings.Join(c.Test.Includes, ","); got != "**/*Test.java,**/*IT.java" {
		t.Errorf("includes = %q", got)
	}
	if got := strings.Join(c.Test.Excludes, ","); got != "**/Slow*.java,**/Flaky*.java" {
		t.Errorf("multi-line excludes = %q", got)
	}
	if c.Test.Groups != "fast" || c.Test.ExcludedGroups != "slow, flaky" || c.Test.ArgLine != `-Xmx1g -Dfile.encoding="UTF-8"` {
		t.Errorf("test = %+v", c.Test)
	}
	if len(c.Generators) != 2 || c.Generators[0].ID != "proto" || strings.Join(c.Generators[0].Command, " ") != "protoc --java_out=target/gen" || c.Generators[1].ID != "second" {
		t.Errorf("generators = %+v", c.Generators)
	}
	if len(c.Plugins) != 1 || c.Plugins[0].ID != "org.acme:thing" || c.Plugins[0].Version != "1.2" {
		t.Errorf("plugins = %+v", c.Plugins)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", c.Warnings)
	}
}

func TestJVTomlWarnsAboutTypos(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "jv.toml"), []byte("[build]\nrelase = 21\n\n[tset]\nx = 1\n"), 0o644)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(c.Warnings, "; ")
	if !strings.Contains(got, "unknown setting build.relase") || !strings.Contains(got, "unknown setting tset") {
		t.Errorf("warnings = %q", got)
	}
}

func TestJVTomlSyntaxErrorsNameTheLine(t *testing.T) {
	cases := map[string]string{
		"[build]\nrelease = 21\nrelease = 17\n":  "line 3: duplicate key",
		"[build\nrelease = 21\n":                 "line 1: unterminated",
		"[build]\nrelease 21\n":                  "line 2: expected key = value",
		"[test]\nincludes = [\"a\", \n":          "unterminated array",
		"[test]\nargLine = \"oops\n":             "line 2: unterminated string",
		"[build]\nrelease = twenty-one\n":        "line 2: cannot parse value",
		"[build]\nrelease = { a = 1 }\n":         "inline tables are not supported",
		"[test]\nincludes = [1, 2]\n":            "test.includes must be a list of strings",
		"build = 1\n[build]\nrelease = \"21\"\n": "not a table",
	}
	for src, want := range cases {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "jv.toml"), []byte(src), 0o644)
		_, err := Load(dir)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want error containing %q, got %v", src, want, err)
		}
	}
}

func TestTomlStringsAndComments(t *testing.T) {
	doc, err := parseTOML([]byte("a = \"x # not a comment\" # a comment\nb = 'C:\\path'\nc = \"tab\\there\"\nd = [\"a]\", 'b[']\n"))
	if err != nil {
		t.Fatal(err)
	}
	if doc["a"] != "x # not a comment" || doc["b"] != `C:\path` || doc["c"] != "tab\there" {
		t.Errorf("doc = %#v", doc)
	}
	if arr, _ := doc["d"].([]any); len(arr) != 2 || arr[0] != "a]" || arr[1] != "b[" {
		t.Errorf("brackets inside strings must not confuse arrays: %#v", doc["d"])
	}
}
