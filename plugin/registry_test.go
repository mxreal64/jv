// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package plugin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRegistry_Resolve(t *testing.T) {
	ctx := context.Background()

	// 1. Not in cache
	desc, err := Resolve(ctx, "nonexistent-group:nonexistent-art", "1.0.0", nil)
	if err == nil || desc != nil {
		t.Fatalf("expected error for nonexistent plugin, got: %v", desc)
	}

	// 2. Present in cache
	home := t.TempDir()
	t.Setenv("HOME", home)

	cd := cacheDir("my.group:my-art", "2.0.0")
	if err := os.MkdirAll(cd, 0o755); err != nil {
		t.Fatal(err)
	}
	binName := "plugin"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(cd, binName)
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	desc, err = Resolve(ctx, "my.group:my-art", "2.0.0", nil)
	if err != nil {
		t.Fatalf("expected successful resolve from cache, got: %v", err)
	}
	if desc.ID != "my.group:my-art" || desc.Version != "2.0.0" || !desc.NativeAvailable {
		t.Fatalf("unexpected descriptor: %+v", desc)
	}
}
