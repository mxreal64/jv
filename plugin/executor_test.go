// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package plugin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mxreal64/jv/lifecycle"
)

func TestPluginExecutor_Run(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target")
	_ = os.MkdirAll(targetDir, 0o755)

	ext := ".sh"
	if runtime.GOOS == "windows" {
		ext = ".bat"
	}

	script := filepath.Join(dir, "myplugin"+ext)
	var scriptContent string
	if runtime.GOOS == "windows" {
		scriptContent = "@echo off\r\necho {\"message\":\"ok\",\"sourceRoots\":[\"src/gen\"],\"properties\":{\"my.prop\":\"value123\"}}\r\nexit /b 0\r\n"
	} else {
		scriptContent = "#!/bin/sh\ncat > /dev/null\necho '{\"message\":\"ok\",\"sourceRoots\":[\"src/gen\"],\"properties\":{\"my.prop\":\"value123\"}}'\nexit 0\n"
	}
	if err := os.WriteFile(script, []byte(scriptContent), 0o755); err != nil {
		t.Fatal(err)
	}

	desc := &PluginDescriptor{
		ID:              "com.example:my-plugin",
		Version:         "1.0.0",
		Binary:          script,
		NativeAvailable: true,
	}

	exec := &PluginExecutor{
		Descriptor: desc,
		Req: Request{
			ID:     "my-plugin",
			Phase:  "generate-sources",
			Config: map[string]any{"foo": "bar"},
		},
	}

	if exec.Phase() != lifecycle.Phase("generate-sources") {
		t.Fatalf("unexpected phase: %v", exec.Phase())
	}
	if exec.ID() != "my-plugin" {
		t.Fatalf("unexpected id: %v", exec.ID())
	}

	bc := lifecycle.BuildContext{
		TargetDir: targetDir,
		Props:     map[string]string{},
	}

	ctx := context.Background()
	if err := exec.Run(ctx, bc); err != nil {
		t.Fatalf("exec.Run failed: %v", err)
	}

	if bc.Props["my.prop"] != "value123" {
		t.Fatalf("expected prop my.prop='value123', got %q", bc.Props["my.prop"])
	}

	// Second run should be skipped due to incremental state
	if err := exec.Run(ctx, bc); err != nil {
		t.Fatalf("second run failed: %v", err)
	}

	// Test test-sources phase
	execTest := &PluginExecutor{
		Descriptor: desc,
		Req: Request{
			ID:    "my-test-plugin",
			Phase: "generate-test-sources",
		},
	}
	bcTest := lifecycle.BuildContext{
		TargetDir: filepath.Join(dir, "target-test"),
		Props:     map[string]string{},
	}
	if err := execTest.Run(ctx, bcTest); err != nil {
		t.Fatalf("execTest.Run failed: %v", err)
	}
	if bcTest.Props["my.prop"] != "value123" {
		t.Fatalf("expected prop in bcTest: %v", bcTest.Props)
	}

	// Test failing plugin
	failScript := filepath.Join(dir, "fail"+ext)
	var failContent string
	if runtime.GOOS == "windows" {
		failContent = "@echo off\r\nexit /b 1\r\n"
	} else {
		failContent = "#!/bin/sh\nexit 1\n"
	}
	if err := os.WriteFile(failScript, []byte(failContent), 0o755); err != nil {
		t.Fatal(err)
	}
	failDesc := &PluginDescriptor{ID: "f", Binary: failScript}
	failExec := &PluginExecutor{Descriptor: failDesc, Req: Request{ID: "fail-plugin", Phase: "compile"}}
	if err := failExec.Run(ctx, bc); err == nil {
		t.Fatalf("expected error from failing plugin")
	}

	// Test skipping plugin
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
	skipDesc := &PluginDescriptor{ID: "s", Binary: skipScript}
	skipExec := &PluginExecutor{Descriptor: skipDesc, Req: Request{ID: "skip-plugin", Phase: "compile"}}
	if err := skipExec.Run(ctx, bc); err != nil {
		t.Fatalf("expected nil from skipping plugin, got %v", err)
	}
}
