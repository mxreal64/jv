// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package packager

import (
	"archive/zip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestManifestLineWraps(t *testing.T) {
	line := manifestLine("Main-Class", strings.Repeat("a", 200))
	for _, l := range strings.Split(strings.TrimSuffix(line, "\r\n"), "\r\n") {
		if len(l)+2 > 72 {
			t.Errorf("manifest line too long (%d bytes): %q", len(l)+2, l)
		}
	}
}

func jarNames(t *testing.T, file string) []string {
	t.Helper()
	zr, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

func TestJarEmptyAndDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	classes := filepath.Join(dir, "classes") // does not exist yet: an empty jar, not an error
	out := filepath.Join(dir, "out.jar")
	if err := Jar(classes, out, nil); err != nil {
		t.Fatalf("empty jar: %v", err)
	}
	if got := jarNames(t, out); !reflect.DeepEqual(got, []string{"META-INF/", "META-INF/MANIFEST.MF"}) {
		t.Errorf("empty jar entries = %v", got)
	}

	if err := os.MkdirAll(filepath.Join(classes, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(classes, "a", "b", "C.class"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Jar(classes, out, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"META-INF/", "META-INF/MANIFEST.MF", "a/", "a/b/", "a/b/C.class"}
	if got := jarNames(t, out); !reflect.DeepEqual(got, want) {
		t.Errorf("jar entries = %v, want %v", got, want)
	}
}
