// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package compiler

import (
	"os"
	"path/filepath"
	"strings"
)

func readManifest(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			out[line] = true
		}
	}
	return out
}

func resourceManifestPath(dst string) string {
	return filepath.Join(dst, ".jv-resources")
}
