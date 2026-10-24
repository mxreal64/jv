// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package jacoco

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mxreal64/jv/lifecycle"
)

func TestPrepareAgentArgLine(t *testing.T) {
	bc := &lifecycle.BuildContext{
		TargetDir: "/tmp/target",
		Props:     map[string]string{},
	}

	if err := PrepareAgent(bc); err != nil {
		t.Fatal(err)
	}

	expected := "-javaagent:/tmp/target/jacoco-agent.jar=destfile=/tmp/target/jacoco.exec"
	if bc.Props["argLine"] != expected {
		t.Fatalf("expected argLine %q, got %q", expected, bc.Props["argLine"])
	}

	// Test appending to existing argLine
	bc.Props["argLine"] = "-Xmx512m"
	if err := PrepareAgent(bc); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bc.Props["argLine"], "-Xmx512m ") {
		t.Fatalf("expected existing argLine preserved, got %q", bc.Props["argLine"])
	}
}

func TestPrepareAgent_ExtractsFromZip(t *testing.T) {
	dir := t.TempDir()
	sourceZip := filepath.Join(dir, "org.jacoco.agent-0.8.11.jar")
	zf, err := os.Create(sourceZip)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("jacocoagent.jar")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("fake-agent-bytes"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zf.Close()

	targetDir := filepath.Join(dir, "target")
	bc := &lifecycle.BuildContext{
		TargetDir: targetDir,
		CompileCP: []string{sourceZip},
		Props:     map[string]string{},
	}

	if err := PrepareAgent(bc); err != nil {
		t.Fatalf("PrepareAgent failed: %v", err)
	}

	agentDst := filepath.Join(targetDir, "jacoco-agent.jar")
	data, err := os.ReadFile(agentDst)
	if err != nil {
		t.Fatalf("failed to read extracted agent jar: %v", err)
	}
	if string(data) != "fake-agent-bytes" {
		t.Fatalf("unexpected content in extracted agent jar: %q", string(data))
	}
}

func TestGenerateReport_Conditions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "target")
	_ = os.MkdirAll(targetDir, 0o755)

	bc := lifecycle.BuildContext{
		TargetDir:  targetDir,
		ClassesDir: filepath.Join(targetDir, "classes"),
		Dir:        dir,
	}

	// 1. No jacoco.exec -> no-op nil
	if err := GenerateReport(ctx, bc); err != nil {
		t.Fatalf("expected nil when no jacoco.exec, got: %v", err)
	}

	// 2. jacoco.exec exists but no cli jar -> no-op nil
	execFile := filepath.Join(targetDir, "jacoco.exec")
	if err := os.WriteFile(execFile, []byte("fake-exec-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateReport(ctx, bc); err != nil {
		t.Fatalf("expected nil when no cli jar on cp, got: %v", err)
	}

	// 3. cli jar on cp -> runs command
	fakeCliJar := filepath.Join(dir, "org.jacoco.cli-0.8.11.jar")
	if err := os.WriteFile(fakeCliJar, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	bc.TestCP = []string{fakeCliJar}
	if err := GenerateReport(ctx, bc); err != nil {
		t.Fatalf("expected GenerateReport to return nil, got: %v", err)
	}
}
