// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package codegen

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mxreal64/jv/lifecycle"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)
	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	if len(args) > 0 {
		_ = os.WriteFile(args[0], []byte("public class Generated {}\n"), 0o644)
	}
}

func TestGeneratorRunAndIncremental(t *testing.T) {
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")

	dir := t.TempDir()
	outDir := filepath.Join(dir, "target", "generated-sources", "testgen")
	inFile := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(inFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	bc := &lifecycle.BuildContext{
		Dir:       dir,
		TargetDir: filepath.Join(dir, "target"),
		Props: map[string]string{
			"project.basedir": dir,
		},
	}

	genFile := filepath.Join(outDir, "Generated.java")
	g := Generator{
		ID:        "testgen",
		Phase:     "generate-sources",
		Command:   []string{os.Args[0], "-test.run=TestHelperProcess", "--", genFile},
		OutputDir: outDir,
		Inputs:    []string{inFile},
	}

	ctx := context.Background()
	if err := Run(ctx, g, bc); err != nil {
		t.Fatalf("first Run failed: %v", err)
	}

	if len(bc.ExtraSourceRoots) != 1 || bc.ExtraSourceRoots[0] != outDir {
		t.Fatalf("expected ExtraSourceRoots to have outDir, got: %v", bc.ExtraSourceRoots)
	}

	fi, err := os.Stat(genFile)
	if err != nil {
		t.Fatalf("generated file does not exist: %v", err)
	}

	// Run again without changing inputs: should skip
	bc2 := &lifecycle.BuildContext{
		Dir:       dir,
		TargetDir: filepath.Join(dir, "target"),
		Props: map[string]string{
			"project.basedir": dir,
		},
	}

	if err := Run(ctx, g, bc2); err != nil {
		t.Fatalf("second Run failed: %v", err)
	}
	fi2, _ := os.Stat(genFile)
	if !fi2.ModTime().Equal(fi.ModTime()) {
		t.Fatalf("expected command not to be rerun when inputs are unchanged")
	}
}
