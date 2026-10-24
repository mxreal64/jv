// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPluginRunnerEchoAndExitCodes(t *testing.T) {
	dir := t.TempDir()

	// Script that succeeds and returns json response
	okScript := filepath.Join(dir, "ok.sh")
	okContent := `#!/bin/sh
cat > /dev/null
echo '{"message":"hello","sourceRoots":["target/gen"]}'
exit 0
`
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
	failScript := filepath.Join(dir, "fail.sh")
	failContent := `#!/bin/sh
echo "fatal error" >&2
exit 1
`
	if err := os.WriteFile(failScript, []byte(failContent), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = RunPlugin(ctx, failScript, req)
	if err == nil {
		t.Fatalf("expected error from exit 1")
	}

	// Script that exits 2 (skip)
	skipScript := filepath.Join(dir, "skip.sh")
	skipContent := `#!/bin/sh
exit 2
`
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
