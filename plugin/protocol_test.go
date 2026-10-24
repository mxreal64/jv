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

func TestPluginRunnerEchoAndExitCodes(t *testing.T) {
	dir := t.TempDir()

	ext := ".sh"
	if runtime.GOOS == "windows" {
		ext = ".bat"
	}

	// Script that succeeds and returns json response
	okScript := filepath.Join(dir, "ok"+ext)
	var okContent string
	if runtime.GOOS == "windows" {
		okContent = "@echo off\r\necho {\"message\":\"hello\",\"sourceRoots\":[\"target/gen\"]}\r\nexit /b 0\r\n"
	} else {
		okContent = "#!/bin/sh\ncat > /dev/null\necho '{\"message\":\"hello\",\"sourceRoots\":[\"target/gen\"]}'\nexit 0\n"
	}
	if err := os.WriteFile(okScript, []byte(okContent), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	req := Request{ID: "test-plugin", Phase: "generate-sources"}
	resp, err := RunPlugin(ctx, okScript, req)
	if err != nil {
		t.Fatalf("RunPlugin failed: %v", err)
	}
	if resp.Message != "hello" || len(resp.SourceRoots) != 1 || resp.SourceRoots[0] != "target/gen" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// Script that exits 1
	failScript := filepath.Join(dir, "fail"+ext)
	var failContent string
	if runtime.GOOS == "windows" {
		failContent = "@echo off\r\necho fatal error 1>&2\r\nexit /b 1\r\n"
	} else {
		failContent = "#!/bin/sh\necho \"fatal error\" >&2\nexit 1\n"
	}
	if err := os.WriteFile(failScript, []byte(failContent), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = RunPlugin(ctx, failScript, req)
	if err == nil {
		t.Fatalf("expected error from exit 1")
	}

	// Script that exits 2 (skip)
	skipScript := filepath.Join(dir, "skip"+ext)
	var skipContent string
	if runtime.GOOS == "windows" {
		skipContent = "@echo off\r\nexit /b 2\r\n"
	} else {
		skipContent = "#!/bin/sh\nexit 2\n"
	}
	if err := os.WriteFile(skipScript, []byte(skipContent), 0o755); err != nil {
		t.Fatal(err)
	}
	respSkip, err := RunPlugin(ctx, skipScript, req)
	if err != nil {
		t.Fatalf("unexpected error from skip script: %v", err)
	}
	if !respSkip.Skip {
		t.Fatalf("expected Skip=true")
	}
}
