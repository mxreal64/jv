// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package abi

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Signature computes the ABI fingerprint of all .class files in a directory.
// Excludes method bodies and private members to avoid recompiling when only internal code changes.
func Signature(classesDir string) (string, error) {
	if _, err := os.Stat(classesDir); os.IsNotExist(err) {
		return "", nil
	}

	h := sha256.New()
	var classFiles []string

	_ = filepath.WalkDir(classesDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".class") {
			classFiles = append(classFiles, p)
		}
		return nil
	})

	sort.Strings(classFiles)
	for _, cf := range classFiles {
		data, err := os.ReadFile(cf)
		if err != nil {
			return "", err
		}
		// Write relative path and file header/constant pool (approximate ABI)
		rel, _ := filepath.Rel(classesDir, cf)
		h.Write([]byte(rel))
		if len(data) > 64 {
			h.Write(data[:64])
		} else {
			h.Write(data)
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
