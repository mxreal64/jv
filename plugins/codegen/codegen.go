// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package codegen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mxreal64/jv/compiler"
	"github.com/mxreal64/jv/lifecycle"
)

type Generator struct {
	ID        string   // unique id for incremental tracking
	Phase     string   // generate-sources or generate-test-sources
	Command   []string // command + args, ${project.basedir} etc. expanded
	OutputDir string   // directory to add to source roots after running
	Inputs    []string // glob patterns of input files to watch for incremental
}

// Run executes the generator command, tracks inputs incrementally, and adds the output directory to bc source roots.
func Run(ctx context.Context, g Generator, bc *lifecycle.BuildContext) error {
	if len(g.Command) == 0 {
		return nil
	}

	// Substitute properties in command arguments
	expandedCmd := make([]string, len(g.Command))
	for i, arg := range g.Command {
		expandedCmd[i] = string(compiler.SubstituteProperties([]byte(arg), bc.Props))
	}

	outDir := g.OutputDir
	if outDir != "" {
		outDir = string(compiler.SubstituteProperties([]byte(outDir), bc.Props))
	}

	// Incremental check
	stateFile := filepath.Join(bc.TargetDir, ".jv", fmt.Sprintf("gen-%s.state", g.ID))
	inc := &lifecycle.Incremental{StateFile: stateFile}

	var inputFiles []any
	for _, in := range g.Inputs {
		expIn := string(compiler.SubstituteProperties([]byte(in), bc.Props))
		matches, _ := filepath.Glob(expIn)
		for _, m := range matches {
			inputFiles = append(inputFiles, m)
		}
	}
	inputFiles = append(inputFiles, expandedCmd)

	changed, err := inc.Changed(inputFiles...)
	if err == nil && !changed {
		// Output dir exists and inputs unchanged
		if outDir != "" {
			if st, err := os.Stat(outDir); err == nil && st.IsDir() {
				if g.Phase == "generate-test-sources" {
					bc.ExtraTestSourceRoots = append(bc.ExtraTestSourceRoots, outDir)
				} else {
					bc.ExtraSourceRoots = append(bc.ExtraSourceRoots, outDir)
				}
				return nil
			}
		}
	}

	if outDir != "" {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
	}

	cmd := exec.CommandContext(ctx, expandedCmd[0], expandedCmd[1:]...)
	cmd.Dir = bc.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generator %s failed (%v): %s", g.ID, err, string(out))
	}

	_ = inc.Done(inputFiles...)

	if outDir != "" {
		if g.Phase == "generate-test-sources" {
			bc.ExtraTestSourceRoots = append(bc.ExtraTestSourceRoots, outDir)
		} else {
			bc.ExtraSourceRoots = append(bc.ExtraSourceRoots, outDir)
		}
	}

	return nil
}
