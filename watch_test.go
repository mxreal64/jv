// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mxreal64/jv/resolver"
)

// makeModule is a test helper that creates a fake Module with source roots on disk.
func makeModule(t *testing.T, dir string) *resolver.Module {
	t.Helper()
	src := filepath.Join(dir, "src", "main", "java")
	_ = os.MkdirAll(src, 0o755)
	return &resolver.Module{
		Dir:         dir,
		Classes:     filepath.Join(dir, "target", "classes"),
		TestClasses: filepath.Join(dir, "target", "test-classes"),
	}
}

func TestScanMtimes_Empty(t *testing.T) {
	dir := t.TempDir()
	m := makeModule(t, dir)
	mtimes := scanMtimes([]*resolver.Module{m})
	if len(mtimes) != 0 {
		t.Fatalf("expected no mtimes for empty src tree, got %d", len(mtimes))
	}
}

func TestDetectChanges_NewFile(t *testing.T) {
	dir := t.TempDir()
	m := makeModule(t, dir)

	initial := scanMtimes([]*resolver.Module{m})
	if len(initial) != 0 {
		t.Fatalf("expected empty initial snapshot")
	}

	// Create a new Java file.
	src := filepath.Join(dir, "src", "main", "java", "Hello.java")
	if err := os.WriteFile(src, []byte("public class Hello {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, next := detectChanges([]*resolver.Module{m}, initial)
	if len(changed) != 1 {
		t.Fatalf("expected 1 changed file, got %d", len(changed))
	}
	if _, ok := changed[src]; !ok {
		t.Fatalf("expected %s to be detected as changed", src)
	}
	if _, ok := next[src]; !ok {
		t.Fatalf("expected %s in updated snapshot", src)
	}
}

func TestDetectChanges_ModifiedFile(t *testing.T) {
	dir := t.TempDir()
	m := makeModule(t, dir)
	src := filepath.Join(dir, "src", "main", "java", "App.java")
	if err := os.WriteFile(src, []byte("public class App {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	initial := scanMtimes([]*resolver.Module{m})
	if len(initial) != 1 {
		t.Fatalf("expected 1 file in snapshot")
	}

	// Wait a moment and touch the file (update mtime).
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(src, []byte("public class App { /* changed */ }"), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, _ := detectChanges([]*resolver.Module{m}, initial)
	if len(changed) != 1 {
		t.Fatalf("expected 1 changed file after modification, got %d", len(changed))
	}
}

func TestDetectChanges_DeletedFile(t *testing.T) {
	dir := t.TempDir()
	m := makeModule(t, dir)
	src := filepath.Join(dir, "src", "main", "java", "Gone.java")
	if err := os.WriteFile(src, []byte("public class Gone {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	initial := scanMtimes([]*resolver.Module{m})
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	changed, _ := detectChanges([]*resolver.Module{m}, initial)
	if _, ok := changed[src]; !ok {
		t.Fatalf("expected deleted file %s to appear as changed", src)
	}
}

func TestDetectChanges_NoChange(t *testing.T) {
	dir := t.TempDir()
	m := makeModule(t, dir)
	src := filepath.Join(dir, "src", "main", "java", "Stable.java")
	if err := os.WriteFile(src, []byte("public class Stable {}"), 0o644); err != nil {
		t.Fatal(err)
	}

	initial := scanMtimes([]*resolver.Module{m})
	changed, _ := detectChanges([]*resolver.Module{m}, initial)
	if len(changed) != 0 {
		t.Fatalf("expected no changes after immediate re-scan, got %d", len(changed))
	}
}

func TestModuleOwnsFile(t *testing.T) {
	dir := t.TempDir()
	m := makeModule(t, dir)

	inside := filepath.Join(dir, "src", "main", "java", "Foo.java")
	outside := "/other/project/src/main/java/Bar.java"

	if !moduleOwnsFile(m, map[string]struct{}{inside: {}}) {
		t.Errorf("expected module to own file inside its src root")
	}
	if moduleOwnsFile(m, map[string]struct{}{outside: {}}) {
		t.Errorf("expected module NOT to own file outside its src root")
	}
}

func TestDirtyModules_DirectChange(t *testing.T) {
	root := t.TempDir()
	m1 := makeModule(t, filepath.Join(root, "core"))
	m2 := makeModule(t, filepath.Join(root, "app"))
	ordered := []*resolver.Module{m1, m2}
	abiSnap := map[string]string{}

	// A file in m1's src changes.
	changedFile := filepath.Join(m1.Dir, "src", "main", "java", "Core.java")
	changed := map[string]struct{}{changedFile: {}}

	dirty := dirtyModules(ordered, changed, abiSnap)
	if len(dirty) == 0 {
		t.Fatal("expected at least m1 to be dirty")
	}
	found := false
	for _, m := range dirty {
		if m == m1 {
			found = true
		}
	}
	if !found {
		t.Error("expected m1 to be in dirty set")
	}
}

func TestModuleSrcRoots_DefaultLayout(t *testing.T) {
	dir := t.TempDir()
	m := &resolver.Module{Dir: dir}
	roots := moduleSrcRoots(m)
	if len(roots) == 0 {
		t.Fatal("expected non-empty src roots for default layout")
	}
	for _, r := range roots {
		if !filepath.IsAbs(r) {
			t.Errorf("expected absolute src root path, got %q", r)
		}
	}
}
