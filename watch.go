// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mxreal64/jv/compiler/abi"
	"github.com/mxreal64/jv/resolver"
)

// watchOpts holds the options parsed for `jv watch`.
type watchOpts struct {
	goal     string        // lifecycle goal to repeat (default: "test")
	cliOpts  *cliOpts      // forwarded CLI flags (-pl, -offline, etc.)
	interval time.Duration // poll interval (default: 500ms)
}

// runWatch is the entry-point for `jv watch [goal]`.
// It polls source directories for mtime changes and re-runs the build goal on each change.
func runWatch(ctx context.Context, wo watchOpts, loader *resolver.Loader, ordered []*resolver.Module) int {
	if wo.interval <= 0 {
		wo.interval = 500 * time.Millisecond
	}

	// Snapshot the ABI of every module's output directory at startup.
	abiSnap := snapshotABIs(ordered)

	// Record the last-seen mtime of every source file we care about.
	mtimes := scanMtimes(ordered)

	logf("[watch] watching %d module(s) — goal: %s (Ctrl-C to stop)", len(ordered), wo.goal)

	// Run an initial build so the user gets immediate feedback.
	runWatchCycle(ctx, wo, loader, ordered, abiSnap)

	ticker := time.NewTicker(wo.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logf("[watch] stopped")
			return 0
		case <-ticker.C:
			changed, newMtimes := detectChanges(ordered, mtimes)
			if len(changed) == 0 {
				continue
			}
			mtimes = newMtimes

			// Determine which modules are dirty (changed sources) and which are
			// downstream of a dirty module whose ABI changed.
			dirty := dirtyModules(ordered, changed, abiSnap)
			if len(dirty) == 0 {
				continue
			}

			clearScreen()
			logf("[watch] %d file(s) changed — rebuilding %d module(s)…", len(changed), len(dirty))

			runWatchCycle(ctx, wo, loader, dirty, abiSnap)
		}
	}
}

// runWatchCycle runs one build cycle and updates the ABI snapshot.
func runWatchCycle(ctx context.Context, wo watchOpts, loader *resolver.Loader, targets []*resolver.Module, abiSnap map[string]string) {
	start := time.Now()

	// Restrict to selected modules if -pl was set.
	sel := targets
	if len(wo.cliOpts.pl) > 0 {
		var err error
		root := targets[0].Dir // best-effort root
		if sel, err = resolver.SelectOnly(targets, root, wo.cliOpts.pl); err != nil {
			logf("[watch] ✗ module selection: %v", err)
			return
		}
	}

	if err := buildTargets(ctx, loader, sel, wo.goal, wo.cliOpts); err != nil {
		if ctx.Err() != nil {
			return
		}
		elapsed := time.Since(start).Round(time.Millisecond)
		printWatchStatus(false, wo.goal, elapsed, err)
		return
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	printWatchStatus(true, wo.goal, elapsed, nil)

	// Update ABI snapshot after a successful build.
	for _, m := range sel {
		for _, outDir := range []string{m.Classes, m.TestClasses} {
			if outDir == "" {
				continue
			}
			if sig, err := abi.Signature(outDir); err == nil && sig != "" {
				abiSnap[outDir] = sig
			}
		}
	}
}

// dirtyModules returns the subset of ordered modules that must be rebuilt.
// A module is dirty if any of its source files changed, OR if it transitively
// depends on a dirty module whose ABI (public API fingerprint) changed.
func dirtyModules(ordered []*resolver.Module, changedFiles map[string]struct{}, abiSnap map[string]string) []*resolver.Module {
	// Set of output directories whose ABI changed after last build.
	abiChanged := map[string]bool{}

	// First pass: modules with directly-changed sources.
	directlyDirty := map[*resolver.Module]bool{}
	for _, m := range ordered {
		if moduleOwnsFile(m, changedFiles) {
			directlyDirty[m] = true
			// Mark the module's output dirs as "potentially ABI-changed" until rebuilt.
			abiChanged[m.Classes] = true
			abiChanged[m.TestClasses] = true
		}
	}
	if len(directlyDirty) == 0 {
		return nil
	}

	// Second pass: propagate downstream using ABI fingerprints.
	// A downstream module only needs rebuilding if its upstream's ABI actually changed.
	// Since we haven't rebuilt yet, we conservatively include any module that
	// compiles against a dirty module's output directory.
	dirty := map[*resolver.Module]bool{}
	for _, m := range ordered {
		if directlyDirty[m] {
			dirty[m] = true
			continue
		}
		// Check whether any classpath entry of this module is in the ABI-changed set.
		if moduleDependsOn(m, abiChanged) {
			dirty[m] = true
		}
	}

	var result []*resolver.Module
	for _, m := range ordered { // preserve dependency order
		if dirty[m] {
			result = append(result, m)
		}
	}
	return result
}

// moduleOwnsFile reports whether the module m contains any of the changed files.
func moduleOwnsFile(m *resolver.Module, changedFiles map[string]struct{}) bool {
	srcRoots := moduleSrcRoots(m)
	for f := range changedFiles {
		for _, root := range srcRoots {
			if strings.HasPrefix(f, root+string(os.PathSeparator)) || f == root {
				return true
			}
		}
	}
	return false
}

// moduleDependsOn reports whether m has any output directory from abiChanged on its classpath.
// We approximate by checking whether the module's Dir is a prefix of any abiChanged key,
// or by looking at the sibling module structure (Classes/TestClasses of other modules).
func moduleDependsOn(m *resolver.Module, abiChanged map[string]bool) bool {
	// The compile classpath of m will contain Classes dirs of modules it depends on.
	// Since we don't have the full resolved classpath here, we approximate: if any
	// module whose output is in abiChanged shares a common parent with m, we include m.
	// A more precise check would require traversing resolver.Artifact lists.
	// For now: check if Classes/TestClasses of m is NOT in abiChanged (then we may need
	// to rebuild it if its upstream changed). We do this by checking the module's
	// target/classes path relative to other dirty module dirs.
	//
	// Practical heuristic that works for the common reactor layout:
	// if any entry in abiChanged is in a sibling module under the same parent, include m.
	for dir := range abiChanged {
		if dir == "" {
			continue
		}
		// dir is e.g. /project/core/target/classes
		// m.Classes is e.g. /project/app/target/classes
		// They share /project as a common grandparent — both are siblings.
		if filepath.Dir(filepath.Dir(filepath.Dir(dir))) ==
			filepath.Dir(filepath.Dir(filepath.Dir(m.Classes))) {
			return true
		}
	}
	return false
}

// moduleSrcRoots returns the source directories jv compiles for module m.
func moduleSrcRoots(m *resolver.Module) []string {
	var roots []string
	// Standard Maven layout (overrides via build/sourceDirectory honoured by jv).
	if m.Eff != nil {
		if m.Eff.Build.SourceDirectory != "" {
			roots = append(roots, m.Eff.Build.SourceDirectory)
		}
		if m.Eff.Build.TestSourceDirectory != "" {
			roots = append(roots, m.Eff.Build.TestSourceDirectory)
		}
	}
	if len(roots) == 0 {
		roots = []string{
			filepath.Join(m.Dir, "src", "main", "java"),
			filepath.Join(m.Dir, "src", "test", "java"),
			filepath.Join(m.Dir, "src", "main", "resources"),
			filepath.Join(m.Dir, "src", "test", "resources"),
		}
	}
	return roots
}

// scanMtimes walks all source directories of all modules and records every file's mtime.
func scanMtimes(modules []*resolver.Module) map[string]time.Time {
	mtimes := make(map[string]time.Time)
	for _, m := range modules {
		for _, root := range moduleSrcRoots(m) {
			_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				if info, e := d.Info(); e == nil {
					mtimes[path] = info.ModTime()
				}
				return nil
			})
		}
	}
	return mtimes
}

// detectChanges scans all module source roots, compares with the previous mtime snapshot,
// and returns the set of files that were added or modified, plus the updated snapshot.
func detectChanges(modules []*resolver.Module, prev map[string]time.Time) (map[string]struct{}, map[string]time.Time) {
	current := make(map[string]time.Time, len(prev))
	changed := make(map[string]struct{})

	for _, m := range modules {
		for _, root := range moduleSrcRoots(m) {
			_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				info, e := d.Info()
				if e != nil {
					return nil
				}
				mtime := info.ModTime()
				current[path] = mtime
				if prev[path] != mtime {
					changed[path] = struct{}{}
				}
				return nil
			})
		}
	}

	// Also detect deletions.
	for path := range prev {
		if _, ok := current[path]; !ok {
			changed[path] = struct{}{}
		}
	}

	return changed, current
}

// snapshotABIs reads the .jv-abi fingerprint from every module output directory.
func snapshotABIs(modules []*resolver.Module) map[string]string {
	snap := make(map[string]string)
	for _, m := range modules {
		for _, outDir := range []string{m.Classes, m.TestClasses} {
			if outDir == "" {
				continue
			}
			if sig, err := abi.Signature(outDir); err == nil && sig != "" {
				snap[outDir] = sig
			}
		}
	}
	return snap
}

// printWatchStatus prints a compact one-line build result with pass/fail indicator.
func printWatchStatus(ok bool, goal string, elapsed time.Duration, err error) {
	ts := time.Now().Format("15:04:05")
	if ok {
		fmt.Fprintf(os.Stderr, "\033[32m✔ [%s] %s — PASS (%v)\033[0m\n", ts, goal, elapsed)
	} else {
		msg := ""
		if err != nil {
			msg = ": " + err.Error()
		}
		fmt.Fprintf(os.Stderr, "\033[31m✘ [%s] %s — FAIL (%v)%s\033[0m\n", ts, goal, elapsed, msg)
	}
}

// clearScreen clears the terminal using ANSI escape codes.
func clearScreen() {
	fmt.Fprint(os.Stderr, "\033[2J\033[H")
}
