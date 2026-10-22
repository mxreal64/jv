// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package packager

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// safeSplit finds the largest byte slice boundary <= maxBytes that does not split a UTF-8 rune.
func safeSplit(b []byte, maxBytes int) int {
	if len(b) <= maxBytes {
		return len(b)
	}
	n := maxBytes
	for n > 0 && !utf8.RuneStart(b[n]) {
		n--
	}
	if n == 0 {
		return maxBytes
	}
	return n
}

// manifestLine wraps a header at 72 bytes (including CRLF) per the JAR specification.
// The initial line can have up to 70 bytes of content (+ 2 bytes \r\n = 72).
// Subsequent continuation lines begin with a space and can have up to 69 bytes of content (+ 2 bytes \r\n = 72).
func manifestLine(k, v string) string {
	raw := []byte(k + ": " + v)
	var out strings.Builder
	first := true

	for len(raw) > 0 {
		if first {
			n := safeSplit(raw, 70)
			out.Write(raw[:n])
			out.WriteString("\r\n")
			raw = raw[n:]
			first = false
		} else {
			out.WriteString(" ")
			n := safeSplit(raw, 69)
			out.Write(raw[:n])
			out.WriteString("\r\n")
			raw = raw[n:]
		}
	}
	return out.String()
}

// Jar writes a reproducible jar (sorted entries, fixed timestamps) from classesDir.
// attrs are extra manifest headers, e.g. {"Main-Class", "app.Main"}.
func Jar(classesDir, outFile string, attrs [][2]string) error {
	if err := os.MkdirAll(classesDir, 0o755); err != nil { // no sources/resources => an empty jar, like Maven
		return err
	}
	var files []string
	err := filepath.WalkDir(classesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() == ".jv-stamp" {
			return nil
		}
		rel, _ := filepath.Rel(classesDir, p)
		if rel = filepath.ToSlash(rel); rel != "META-INF/MANIFEST.MF" {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	dirSet := map[string]bool{}
	for _, name := range files {
		for d := path.Dir(name); d != "." && d != "/"; d = path.Dir(d) {
			dirSet[d+"/"] = true
		}
	}
	delete(dirSet, "META-INF/")
	dirs := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	if err := os.MkdirAll(filepath.Dir(outFile), 0o755); err != nil {
		return err
	}
	tmp := outFile + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	fixed := time.Date(1980, 2, 1, 0, 0, 0, 0, time.UTC)
	add := func(name string, r io.Reader) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: fixed})
		if err != nil {
			return err
		}
		_, err = io.Copy(w, r)
		return err
	}
	mf := "Manifest-Version: 1.0\r\nCreated-By: jv\r\n"
	for _, a := range attrs {
		mf += manifestLine(a[0], a[1])
	}
	werr := add("META-INF/", strings.NewReader(""))
	if werr == nil {
		werr = add("META-INF/MANIFEST.MF", strings.NewReader(mf+"\r\n"))
	}
	for _, d := range dirs { // directory entries, as Maven's jars have
		if werr != nil {
			break
		}
		werr = add(d, strings.NewReader(""))
	}
	for _, name := range files {
		if werr != nil {
			break
		}
		in, err := os.Open(filepath.Join(classesDir, filepath.FromSlash(name)))
		if err != nil {
			werr = err
			break
		}
		werr = add(name, in)
		in.Close()
	}
	if cerr := zw.Close(); werr == nil {
		werr = cerr
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(tmp)
		return werr
	}
	return os.Rename(tmp, outFile)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".jv-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// Install copies the jar (if any) and POM into a Maven-layout local repository.
func Install(repo, g, a, v, jar, pomFile string) error {
	dir := filepath.Join(repo, filepath.FromSlash(strings.ReplaceAll(g, ".", "/")), a, v)
	if jar != "" {
		if err := copyFile(jar, filepath.Join(dir, fmt.Sprintf("%s-%s.jar", a, v))); err != nil {
			return err
		}
	}
	return copyFile(pomFile, filepath.Join(dir, fmt.Sprintf("%s-%s.pom", a, v)))
}
