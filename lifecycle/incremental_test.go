// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package lifecycle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIncremental_changedOnMissingState(t *testing.T) {
	inc := &Incremental{StateFile: filepath.Join(t.TempDir(), "state.json")}
	changed, err := inc.Changed("hello")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("missing state file must report changed")
	}
}

func TestIncremental_notChangedAfterDone(t *testing.T) {
	inc := &Incremental{StateFile: filepath.Join(t.TempDir(), "state.json")}
	if err := inc.Done("hello", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	changed, err := inc.Changed("hello", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("same inputs must not report changed")
	}
}

func TestIncremental_changedAfterInputMutation(t *testing.T) {
	inc := &Incremental{StateFile: filepath.Join(t.TempDir(), "state.json")}
	if err := inc.Done("hello"); err != nil {
		t.Fatal(err)
	}
	changed, err := inc.Changed("hello", "extra")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("extra input must report changed")
	}
}

func TestIncremental_sliceOrderIndependent(t *testing.T) {
	inc := &Incremental{StateFile: filepath.Join(t.TempDir(), "state.json")}
	if err := inc.Done([]string{"b", "a"}); err != nil {
		t.Fatal(err)
	}
	// []string is sorted before hashing, so order must not matter
	changed, err := inc.Changed([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("reordered slice must not report changed")
	}
}

func TestIncremental_filesIn_noChange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "A.java"), []byte("class A {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	inc := &Incremental{StateFile: filepath.Join(t.TempDir(), "state.json")}
	if err := inc.Done(FilesIn{Dir: dir, Glob: "**/*.java"}); err != nil {
		t.Fatal(err)
	}
	changed, err := inc.Changed(FilesIn{Dir: dir, Glob: "**/*.java"})
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("no file change must not report changed")
	}
}

func TestIncremental_filesIn_detectsEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "A.java")
	if err := os.WriteFile(path, []byte("class A {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	inc := &Incremental{StateFile: filepath.Join(t.TempDir(), "state.json")}
	if err := inc.Done(FilesIn{Dir: dir, Glob: "**/*.java"}); err != nil {
		t.Fatal(err)
	}
	// Rewrite with different content (different size so mtime change is guaranteed).
	if err := os.WriteFile(path, []byte("class A { int x; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := inc.Changed(FilesIn{Dir: dir, Glob: "**/*.java"})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("file edit must report changed")
	}
}

func TestIncremental_filesIn_missingDirOk(t *testing.T) {
	inc := &Incremental{StateFile: filepath.Join(t.TempDir(), "state.json")}
	// A non-existent source dir should not error; treated as empty set.
	if err := inc.Done(FilesIn{Dir: "/nonexistent/path", Glob: "**/*.java"}); err != nil {
		t.Fatal(err)
	}
}

func TestIncremental_outputs(t *testing.T) {
	inc := &Incremental{StateFile: filepath.Join(t.TempDir(), "state.json")}
	if err := inc.Done("x"); err != nil {
		t.Fatal(err)
	}
	if err := inc.RecordOutputs([]string{"/a", "/b"}); err != nil {
		t.Fatal(err)
	}
	outs, err := inc.Outputs()
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 2 || outs[0] != "/a" || outs[1] != "/b" {
		t.Fatalf("unexpected outputs: %v", outs)
	}
}

func TestIncremental_outputsNilOnMissing(t *testing.T) {
	inc := &Incremental{StateFile: filepath.Join(t.TempDir(), "state.json")}
	outs, err := inc.Outputs()
	if err != nil || outs != nil {
		t.Fatalf("missing state file must return nil,nil: %v %v", outs, err)
	}
}

func TestAntMatch(t *testing.T) {
	cases := []struct {
		pat, s string
		want   bool
	}{
		{"**/*.java", "Foo.java", true},
		{"**/*.java", "com/example/Foo.java", true},
		{"**/*.java", "Foo.class", false},
		{"*Test.java", "FooTest.java", true},
		{"*Test.java", "com/FooTest.java", false}, // * doesn't cross /
		{"**/*Test.java", "com/example/FooTest.java", true},
		{"*.java", "Foo.java", true},
		{"*.java", "Foo.class", false},
		{"com/**", "com/example/Bar.java", true},
		{"com/**", "org/example/Bar.java", false},
	}
	for _, c := range cases {
		got := antMatch(c.pat, c.s)
		if got != c.want {
			t.Errorf("antMatch(%q, %q) = %v, want %v", c.pat, c.s, got, c.want)
		}
	}
}
