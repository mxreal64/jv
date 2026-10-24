// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package shade

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func createTestJar(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// craftMinimalClass creates a valid minimal class file with a CONSTANT_Utf8 entry.
func craftMinimalClass(utf8Str string) []byte {
	var buf bytes.Buffer
	// magic 0xCAFEBABE
	buf.Write([]byte{0xCA, 0xFE, 0xBA, 0xBE})
	// minor=0, major=52 (Java 8)
	buf.Write([]byte{0x00, 0x00, 0x00, 0x34})
	// cpCount = 2 (index 1 is Utf8)
	buf.Write([]byte{0x00, 0x02})
	// tag = 1 (Utf8)
	buf.WriteByte(1)
	uLen := uint16(len(utf8Str))
	_ = binary.Write(&buf, binary.BigEndian, uLen)
	buf.WriteString(utf8Str)
	// access_flags (2 bytes) = 0
	buf.Write([]byte{0x00, 0x00})
	// this_class (2 bytes) = 0
	buf.Write([]byte{0x00, 0x00})
	// super_class (2 bytes) = 0
	buf.Write([]byte{0x00, 0x00})
	// interfaces_count = 0
	buf.Write([]byte{0x00, 0x00})
	// fields_count = 0
	buf.Write([]byte{0x00, 0x00})
	// methods_count = 0
	buf.Write([]byte{0x00, 0x00})
	// attributes_count = 0
	buf.Write([]byte{0x00, 0x00})

	return buf.Bytes()
}

func TestRewriteClass(t *testing.T) {
	origStr := "com/google/common/collect/ImmutableList"
	data := craftMinimalClass(origStr)

	rules := []RelocateRule{
		{
			Pattern:  "com.google.common",
			Shadedto: "shaded.com.google.common",
		},
	}

	rewritten, changed, err := RewriteClass(data, rules)
	if err != nil {
		t.Fatalf("RewriteClass error: %v", err)
	}
	if !changed {
		t.Fatalf("expected class to be rewritten")
	}

	expectedStr := "shaded/com/google/common/collect/ImmutableList"
	if !bytes.Contains(rewritten, []byte(expectedStr)) {
		t.Fatalf("expected relocated string in class bytes, got:\n%s", string(rewritten))
	}
}

func TestServicesTransformer(t *testing.T) {
	trans := &ServicesResourceTransformer{}
	s1 := []byte("com.example.Impl1\ncom.example.Impl2\n")
	s2 := []byte("com.example.Impl2\ncom.example.Impl3\n")

	merged, err := trans.Transform("META-INF/services/com.example.Service", s1, s2)
	if err != nil {
		t.Fatalf("Transform error: %v", err)
	}
	expected := "com.example.Impl1\ncom.example.Impl2\ncom.example.Impl3\n"
	if string(merged) != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, string(merged))
	}
}

func TestMergeJarsWithFilters(t *testing.T) {
	dir := t.TempDir()
	j1 := filepath.Join(dir, "j1.jar")
	j2 := filepath.Join(dir, "j2.jar")
	out := filepath.Join(dir, "out.jar")

	createTestJar(t, j1, map[string][]byte{
		"com/foo/A.class":                       craftMinimalClass("com/foo/A"),
		"META-INF/services/com.example.Service": []byte("com.foo.S1\n"),
		"unwanted.txt":                          []byte("skip me"),
	})

	createTestJar(t, j2, map[string][]byte{
		"com/foo/B.class":                       craftMinimalClass("com/foo/B"),
		"META-INF/services/com.example.Service": []byte("com.foo.S2\n"),
	})

	filters := []Filter{
		{
			Artifact: "*:*",
			Excludes: []string{"*.txt"},
		},
	}

	rules := []RelocateRule{
		{
			Pattern:  "com.foo",
			Shadedto: "my.shaded.foo",
		},
	}

	if err := Merge([]string{j1, j2}, out, rules, filters, nil); err != nil {
		t.Fatalf("Merge error: %v", err)
	}

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		entries[f.Name] = f
	}

	if _, ok := entries["unwanted.txt"]; ok {
		t.Errorf("unwanted.txt should have been filtered out")
	}
	if _, ok := entries["my/shaded/foo/A.class"]; !ok {
		t.Errorf("expected relocated entry my/shaded/foo/A.class")
	}
	if _, ok := entries["my/shaded/foo/B.class"]; !ok {
		t.Errorf("expected relocated entry my/shaded/foo/B.class")
	}

	svc := entries["META-INF/services/com.example.Service"]
	if svc == nil {
		t.Fatalf("missing service file")
	}
	rc, _ := svc.Open()
	svcData, _ := io.ReadAll(rc)
	rc.Close()

	expectedSvc := "my.shaded.foo.S1\nmy.shaded.foo.S2\n"
	if string(svcData) != expectedSvc {
		t.Fatalf("expected service:\n%s\ngot:\n%s", expectedSvc, string(svcData))
	}
}
