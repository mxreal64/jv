// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package compiler

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mxreal64/jv/lifecycle"
)

var binaryExtensions = map[string]bool{
	".jar":   true,
	".zip":   true,
	".class": true,
	".png":   true,
	".jpg":   true,
	".jpeg":  true,
	".gif":   true,
	".bmp":   true,
	".ico":   true,
	".pdf":   true,
	".ttf":   true,
	".woff":  true,
	".woff2": true,
	".eot":   true,
	".svg":   true,
	".war":   true,
	".ear":   true,
	".so":    true,
	".dll":   true,
	".exe":   true,
	".bin":   true,
}

// SubstituteProperties replaces ${key} and @key@ with values from props.
// Unknown keys are left unchanged.
func SubstituteProperties(content []byte, props map[string]string) []byte {
	if len(props) == 0 || len(content) == 0 {
		return content
	}

	res := content
	// 1. Substitute ${key}
	for range 10 {
		idx := bytes.Index(res, []byte("${"))
		if idx < 0 {
			break
		}
		var buf bytes.Buffer
		buf.Grow(len(res))
		rest := res
		changed := false
		for {
			s := bytes.Index(rest, []byte("${"))
			if s < 0 {
				buf.Write(rest)
				break
			}
			e := bytes.IndexByte(rest[s:], '}')
			if e < 0 {
				buf.Write(rest)
				break
			}
			buf.Write(rest[:s])
			key := string(rest[s+2 : s+e])
			if v, ok := props[key]; ok && key != "" {
				buf.WriteString(v)
				changed = true
			} else {
				buf.Write(rest[s : s+e+1])
			}
			rest = rest[s+e+1:]
		}
		res = buf.Bytes()
		if !changed {
			break
		}
	}

	// 2. Substitute @key@
	for range 10 {
		idx := bytes.IndexByte(res, '@')
		if idx < 0 {
			break
		}
		var buf bytes.Buffer
		buf.Grow(len(res))
		rest := res
		changed := false
		for {
			s := bytes.IndexByte(rest, '@')
			if s < 0 {
				buf.Write(rest)
				break
			}
			e := bytes.IndexByte(rest[s+1:], '@')
			if e < 0 {
				buf.Write(rest)
				break
			}
			e += s + 1 // absolute index of closing '@'
			key := string(rest[s+1 : e])
			if v, ok := props[key]; ok && key != "" && !strings.ContainsAny(key, " \t\r\n") {
				buf.Write(rest[:s])
				buf.WriteString(v)
				changed = true
				rest = rest[e+1:]
			} else {
				buf.Write(rest[:s+1])
				rest = rest[s+1:]
			}
		}
		res = buf.Bytes()
		if !changed {
			break
		}
	}

	return res
}

// CopyResources copies src into dst, optionally filtering properties in non-binary files.
func CopyResources(ctx context.Context, src, dst string, filtering bool, props map[string]string, inc *lifecycle.Incremental) error {
	manifestPath := resourceManifestPath(dst)
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		if old := readManifest(manifestPath); len(old) > 0 {
			for rel := range old {
				_ = os.Remove(filepath.Join(dst, rel))
			}
			pruneEmptyDirs(dst)
			_ = os.Remove(manifestPath)
		}
		return nil
	}

	oldManifest := readManifest(manifestPath)
	newManifest := map[string]bool{}

	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		newManifest[rel] = true

		si, err := d.Info()
		if err != nil {
			return err
		}

		ext := strings.ToLower(filepath.Ext(p))
		isBinary := binaryExtensions[ext]

		if !filtering || isBinary {
			// Fast path: verbatim copy
			if ti, err := os.Stat(target); err == nil && ti.Size() == si.Size() && ti.ModTime().Equal(si.ModTime()) {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if err := os.WriteFile(target, data, 0o644); err != nil {
				return err
			}
			_ = os.Chtimes(target, si.ModTime(), si.ModTime())
			return nil
		}

		// Filtering path
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		filtered := SubstituteProperties(raw, props)

		// Write to dst only if content differs from existing file
		if cur, err := os.ReadFile(target); err == nil && bytes.Equal(cur, filtered) {
			return nil
		}

		if err := os.WriteFile(target, filtered, 0o644); err != nil {
			return err
		}
		_ = os.Chtimes(target, si.ModTime(), si.ModTime())
		return nil
	})
	if err != nil {
		return err
	}

	for rel := range oldManifest {
		if !newManifest[rel] {
			_ = os.Remove(filepath.Join(dst, rel))
		}
	}
	if len(oldManifest) > 0 {
		pruneEmptyDirs(dst)
	}

	lines := make([]string, 0, len(newManifest))
	for rel := range newManifest {
		lines = append(lines, rel)
	}
	sort.Strings(lines)
	_ = os.WriteFile(manifestPath, []byte(strings.Join(lines, "\n")), 0o644)
	return nil
}
