// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package codegen

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

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

	// Arguments such as src/main/proto/*.proto are expanded here, as a shell would; exec does not.
	expandedCmd = expandGlobArgs(expandedCmd, bc.Dir)

	outDir := g.OutputDir
	if outDir != "" {
		outDir = string(compiler.SubstituteProperties([]byte(outDir), bc.Props))
		if !filepath.IsAbs(outDir) && bc.Dir != "" {
			outDir = filepath.Join(bc.Dir, outDir)
		}
	}
	isTest := g.Phase == "generate-test-sources"

	// Incremental check
	stateFile := filepath.Join(bc.TargetDir, ".jv", fmt.Sprintf("gen-%s.state", g.ID))
	inc := &lifecycle.Incremental{StateFile: stateFile}

	var inputFiles []any
	for _, in := range g.Inputs {
		expIn := string(compiler.SubstituteProperties([]byte(in), bc.Props))
		if !filepath.IsAbs(expIn) && bc.Dir != "" {
			expIn = filepath.Join(bc.Dir, expIn) // globs are relative to the module, not the process
		}
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
				bc.AddSourceRoot(outDir, isTest)
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
		bc.AddSourceRoot(outDir, isTest)
	}

	return nil
}

// expandGlobArgs replaces every argument that contains a glob pattern (and is not an option) with the
// files it matches, relative to dir. A pattern that matches nothing is left as it is.
func expandGlobArgs(args []string, dir string) []string {
	if len(args) == 0 {
		return args
	}
	out := []string{args[0]}
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "-") || !strings.ContainsAny(a, "*?[") {
			out = append(out, a)
			continue
		}
		pattern := a
		if !filepath.IsAbs(pattern) && dir != "" {
			pattern = filepath.Join(dir, a)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			out = append(out, a)
			continue
		}
		sort.Strings(matches)
		for _, m := range matches {
			if rel, err := filepath.Rel(dir, m); err == nil && dir != "" && !filepath.IsAbs(a) {
				m = rel
			}
			out = append(out, m)
		}
	}
	return out
}
