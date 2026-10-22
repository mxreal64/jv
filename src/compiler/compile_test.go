package compiler

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestParseJavacMajor(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"javac 27\n", 27},
		{"javac 21.0.2\n", 21},
		{"javac 1.8.0_392\n", 8},
		{"javac 25-ea\n", 25},
		{"Picked up JAVA_TOOL_OPTIONS: -x\njavac 17.0.9\n", 17},
		{"something else", 0},
		{"", 0},
	}
	for _, tt := range tests {
		if got := parseJavacMajor(tt.in); got != tt.want {
			t.Errorf("parseJavacMajor(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestHasProcessors(t *testing.T) {
	dir := t.TempDir()

	if hasProcessors([]string{filepath.Join(dir, "missing")}) {
		t.Errorf("missing entry must not report processors")
	}

	// directory entry
	d := filepath.Join(dir, "classes")
	svc := filepath.Join(d, filepath.FromSlash(procService))
	if err := os.MkdirAll(filepath.Dir(svc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc, []byte("x.Proc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasProcessors([]string{d}) {
		t.Errorf("directory with a Processor service file should be detected")
	}

	// jar entries
	mk := func(name string, entries ...string) string {
		p := filepath.Join(dir, name)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		for _, e := range entries {
			w, err := zw.Create(e)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte("x"))
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		f.Close()
		return p
	}
	plain := mk("plain.jar", "a/B.class")
	proc := mk("proc.jar", "a/B.class", procService)
	if hasProcessors([]string{plain}) {
		t.Errorf("plain jar must not report processors")
	}
	if !hasProcessors([]string{plain, proc}) {
		t.Errorf("jar with a Processor service file should be detected")
	}
}
