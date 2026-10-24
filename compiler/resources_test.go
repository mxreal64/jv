// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package compiler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestCopyResourcesRemovesStaleFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a/x.txt", "x")
	write("b.txt", "b")
	// a compiled class that jv did not copy must never be touched
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "App.class"), []byte("class"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopyResources(ctx, src, dst, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dst, "a", "x.txt")) || !exists(filepath.Join(dst, "b.txt")) {
		t.Fatal("resources were not copied")
	}

	// delete a resource (and its directory) from the source
	if err := os.RemoveAll(filepath.Join(src, "a")); err != nil {
		t.Fatal(err)
	}
	if err := CopyResources(ctx, src, dst, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dst, "a", "x.txt")) || exists(filepath.Join(dst, "a")) {
		t.Error("deleted resource (and its empty directory) must disappear from the output")
	}
	if !exists(filepath.Join(dst, "b.txt")) || !exists(filepath.Join(dst, "App.class")) {
		t.Error("unrelated files must be kept")
	}

	// a same-size edit with an older mtime must still be copied
	write("b.txt", "B")
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(src, "b.txt"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := CopyResources(ctx, src, dst, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "b.txt")); string(got) != "B" {
		t.Errorf("edited resource not copied, got %q", got)
	}

	// no resources directory at all: previously copied files are removed too
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	if err := CopyResources(ctx, src, dst, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dst, "b.txt")) {
		t.Error("resources must be removed when the source directory is gone")
	}
	if !exists(filepath.Join(dst, "App.class")) {
		t.Error("App.class must survive")
	}
}

func TestResourceFiltering(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}

	props := map[string]string{
		"project.version": "1.2.3",
		"app.name":        "my-cool-app",
	}

	// 1. .properties file with ${key} and @key@
	propContent := "version=${project.version}\nname=@app.name@\nunknown=${nope}\n"
	if err := os.WriteFile(filepath.Join(src, "application.properties"), []byte(propContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Binary file .png
	pngBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, '$', '{', 'p', 'r', 'o', 'j', 'e', 'c', 't', '.', 'v', 'e', 'r', 's', 'i', 'o', 'n', '}'}
	if err := os.WriteFile(filepath.Join(src, "logo.png"), pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopyResources(ctx, src, dst, true, props, nil); err != nil {
		t.Fatal(err)
	}

	// Verify property substitutions
	gotProp, err := os.ReadFile(filepath.Join(dst, "application.properties"))
	if err != nil {
		t.Fatal(err)
	}
	expectedProp := "version=1.2.3\nname=my-cool-app\nunknown=${nope}\n"
	if string(gotProp) != expectedProp {
		t.Fatalf("expected:\n%s\ngot:\n%s", expectedProp, string(gotProp))
	}

	// Verify binary is untouched
	gotPng, err := os.ReadFile(filepath.Join(dst, "logo.png"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotPng) != string(pngBytes) {
		t.Fatalf("binary file was modified during filtering")
	}
}
