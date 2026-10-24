// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCACache(t *testing.T) {
	dir := t.TempDir()
	ca := &CACache{Dir: dir}

	data := []byte("content-addressed-test-bytes")
	key, err := ca.Put(data)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	got, err := ca.Get(key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("mismatched content")
	}

	dest := filepath.Join(dir, "linked", "dest.txt")
	if err := ca.Link(key, dest); err != nil {
		t.Fatalf("Link failed: %v", err)
	}
	linkedData, _ := os.ReadFile(dest)
	if !bytes.Equal(linkedData, data) {
		t.Fatalf("mismatched linked content")
	}
}
