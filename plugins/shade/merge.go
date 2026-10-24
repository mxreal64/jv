// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package shade

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Merge combines multiple input jars into one shaded jar, applying relocations, filters, and merge strategies.
func Merge(inputs []string, output string, rules []RelocateRule, filters []Filter, merges []MergeStrategy) error {
	tmpOut := output + ".tmp"
	outFile, err := os.Create(tmpOut)
	if err != nil {
		return err
	}
	defer func() {
		outFile.Close()
		_ = os.Remove(tmpOut)
	}()

	zw := zip.NewWriter(outFile)

	// In-memory merged files buffer (e.g. for META-INF/services/...)
	mergedFiles := map[string][]byte{}
	seenFiles := map[string]bool{}

	// Default merge strategy if none provided
	if len(merges) == 0 {
		merges = []MergeStrategy{&ServicesResourceTransformer{}}
	}

	for _, inputJar := range inputs {
		zr, err := zip.OpenReader(inputJar)
		if err != nil {
			return fmt.Errorf("reading jar %s: %w", inputJar, err)
		}

		for _, f := range zr.File {
			name := f.Name

			// Skip directories and duplicate signatures/manifests from dependencies
			if strings.HasSuffix(name, "/") {
				continue
			}
			if strings.HasPrefix(name, "META-INF/") && (strings.HasSuffix(name, ".SF") || strings.HasSuffix(name, ".DSA") || strings.HasSuffix(name, ".RSA")) {
				continue
			}

			// Filter check
			allowed := true
			for _, flt := range filters {
				if !flt.AllowsEntry(name) {
					allowed = false
					break
				}
			}
			if !allowed {
				continue
			}

			rc, err := f.Open()
			if err != nil {
				zr.Close()
				return err
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				zr.Close()
				return err
			}

			// Check merge strategies
			var matchedStrategy MergeStrategy
			for _, ms := range merges {
				if ms.Matches(name) {
					matchedStrategy = ms
					break
				}
			}

			if matchedStrategy != nil {
				merged, err := matchedStrategy.Transform(name, mergedFiles[name], content)
				if err != nil {
					zr.Close()
					return fmt.Errorf("merging %s: %w", name, err)
				}
				mergedFiles[name] = merged
				continue
			}

			// If already seen and not a mergeable file, skip duplicate
			if seenFiles[name] {
				continue
			}
			seenFiles[name] = true

			// Apply bytecode or resource relocation
			outName := name
			outContent := content

			if strings.HasSuffix(name, ".class") {
				relocated, _, err := RewriteClass(content, rules)
				if err == nil {
					outContent = relocated
				}
				// Also relocate entry name if class package moved
				for _, r := range rules {
					if newN, ok := r.relocateString(name); ok {
						outName = newN
						break
					}
				}
			} else {
				ext := filepath.Ext(name)
				relocated, _, err := RewriteResource(content, ext, rules)
				if err == nil {
					outContent = relocated
				}
			}

			w, err := zw.Create(outName)
			if err != nil {
				zr.Close()
				return err
			}
			if _, err := w.Write(outContent); err != nil {
				zr.Close()
				return err
			}
		}
		zr.Close()
	}

	// Write all merged entries
	for path, content := range mergedFiles {
		// Relocate content if text resource
		ext := filepath.Ext(path)
		relocated, _, err := RewriteResource(content, ext, rules)
		if err == nil {
			content = relocated
		}

		w, err := zw.Create(path)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, bytes.NewReader(content)); err != nil {
			return err
		}
	}

	if err := zw.Close(); err != nil {
		return err
	}
	if err := outFile.Close(); err != nil {
		return err
	}

	return os.Rename(tmpOut, output)
}
