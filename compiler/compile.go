// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package compiler

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options are the javac settings jv honours. Args are extra javac arguments (for example
// -Xlint:all or -parameters).
type Options struct {
	Encoding, Release, Source, Target string
	Args                              []string
}

// OptionsFrom reads the usual Maven compiler properties.
func OptionsFrom(p map[string]string) Options {
	o := Options{
		Encoding: p["project.build.sourceEncoding"],
		Release:  p["maven.compiler.release"],
		Source:   p["maven.compiler.source"],
		Target:   p["maven.compiler.target"],
	}
	if o.Release == "" && p["java.version"] != "" {
		o.Release = p["java.version"]
	}
	if o.Encoding == "" {
		o.Encoding = "UTF-8"
	}
	return o
}

func javacBinary() string {
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		p := filepath.Join(jh, "bin", "javac")
		if runtime.GOOS == "windows" {
			p += ".exe"
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return "javac"
}

// Compile compiles every .java under srcDir into outDir with javac. It is skipped
// when sources, classpath, options and the build stamps of any classpath directories
// (sibling modules, target/classes for tests) are unchanged since the last successful run.
// Returns whether javac actually ran.
func Compile(ctx context.Context, srcDir, outDir string, classpath []string, opt Options) (bool, error) {
	var files, metas []string
	var newest time.Time
	_ = filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".java") {
			files = append(files, p)
			if info, e := d.Info(); e == nil {
				metas = append(metas, fmt.Sprintf("%s:%d:%d", p, info.Size(), info.ModTime().UnixNano()))
				if info.ModTime().After(newest) {
					newest = info.ModTime()
				}
			}
		}
		return nil
	})
	if len(files) == 0 {
		return false, nil
	}
	sort.Strings(files)
	sort.Strings(metas)
	cp := strings.Join(classpath, string(os.PathListSeparator))
	var in strings.Builder
	in.WriteString(cp + "\n" + strings.Join(files, "\n") + fmt.Sprintf("\n%+v", opt))
	in.WriteString("\n" + strings.Join(metas, "\n")) // size+mtime of every source: edits with an older mtime still recompile
	in.WriteString("\nJAVA_HOME=" + os.Getenv("JAVA_HOME"))
	for _, e := range classpath { // upstream classes recompiled => recompile
		if filepath.Clean(e) == filepath.Clean(outDir) {
			continue // our own output (test-classes is on its own classpath): its stamp changes every run
		}
		if st, err := os.Stat(filepath.Join(e, ".jv-stamp")); err == nil {
			fmt.Fprintf(&in, "\n%s@%d", e, st.ModTime().UnixNano())
		}
	}
	sum := sha256.Sum256([]byte(in.String()))
	sig := hex.EncodeToString(sum[:])
	stamp := filepath.Join(outDir, ".jv-stamp")
	if data, err := os.ReadFile(stamp); err == nil && string(data) == sig {
		if st, e := os.Stat(stamp); e == nil && !st.ModTime().Before(newest) {
			return false, nil
		}
	}

	_ = os.RemoveAll(outDir) // drop stale classes from deleted sources
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return false, err
	}
	var sb strings.Builder
	for _, f := range files {
		cleanPath := filepath.ToSlash(f)
		cleanPath = strings.ReplaceAll(cleanPath, `"`, `\"`)
		sb.WriteString(`"` + cleanPath + `"` + "\n")
	}
	argFile := filepath.Join(filepath.Dir(outDir), "."+filepath.Base(outDir)+"-sources.txt")
	if err := os.WriteFile(argFile, []byte(sb.String()), 0o644); err != nil {
		return false, err
	}
	defer os.Remove(argFile)

	args := []string{"-d", outDir, "-encoding", opt.Encoding}
	if cp != "" {
		args = append(args, "-cp", cp)
	}
	switch {
	case opt.Release != "":
		args = append(args, "--release", opt.Release)
	default:
		if opt.Source != "" {
			args = append(args, "-source", opt.Source)
		}
		if opt.Target != "" {
			args = append(args, "-target", opt.Target)
		}
	}
	args = append(args, opt.Args...)
	// Start javac's own JVM with flags suited to a short-lived process:
	// - TieredStopAtLevel=1: C1-only JIT; C2 never pays off for a compile this short
	// - UseSerialGC: the cheapest collector for a short-lived, mostly single-threaded run
	// They are JVM flags (-J-...) and do not affect the compiled classes. They are only
	// added when the caller has not set either of them.
	hasJFlags := false
	for _, a := range args {
		if strings.HasPrefix(a, "-J-XX:TieredStopAtLevel") || strings.HasPrefix(a, "-J-XX:+UseSerialGC") {
			hasJFlags = true
			break
		}
	}
	if !hasJFlags {
		args = append([]string{"-J-XX:TieredStopAtLevel=1", "-J-XX:+UseSerialGC"}, args...)
	}
	// javac 23+ no longer runs annotation processors found on the classpath unless asked to.
	// -proc:full (javac 21+) restores what Maven projects expect.
	if hasProcessors(classpath) && javacMajor() >= 21 {
		fmt.Fprintln(os.Stderr, "[jv] annotation processors found on the classpath; compiling with -proc:full")
		args = append(args, "-proc:full")
	}
	args = append(args, "@"+argFile)
	cmd := exec.CommandContext(ctx, javacBinary(), args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return false, err
	}
	return true, os.WriteFile(stamp, []byte(sig), 0o644)
}

// resourceManifestPath returns the manifest file tracking what CopyResources copied.
func resourceManifestPath(dst string) string {
	return filepath.Join(dst, ".jv-resources")
}

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

// CopyResources copies src into dst (no filtering), skipping up-to-date files.
// Tracks copied files in .jv-resources and removes stale files from previous runs.
func CopyResources(src, dst string) error {
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
	if err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
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
		if ti, err := os.Stat(target); err == nil && ti.Size() == si.Size() && ti.ModTime().Equal(si.ModTime()) {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		_ = os.Chtimes(target, si.ModTime(), si.ModTime())
		return nil
	}); err != nil {
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

// pruneEmptyDirs removes empty subdirectories of root (deepest first).
func pruneEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, _ error) error {
		if d != nil && d.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
}

const procService = "META-INF/services/javax.annotation.processing.Processor"

// hasProcessors reports whether any classpath entry registers an annotation processor.
func hasProcessors(classpath []string) bool {
	for _, e := range classpath {
		st, err := os.Stat(e)
		if err != nil {
			continue
		}
		if st.IsDir() {
			if _, err := os.Stat(filepath.Join(e, filepath.FromSlash(procService))); err == nil {
				return true
			}
			continue
		}
		zr, err := zip.OpenReader(e)
		if err != nil {
			continue
		}
		found := false
		for _, f := range zr.File {
			if f.Name == procService {
				found = true
				break
			}
		}
		zr.Close()
		if found {
			return true
		}
	}
	return false
}

// parseJavacMajor extracts the major version from `javac -version` output
// ("javac 27", "javac 21.0.2", "javac 1.8.0_392", "javac 25-ea"); 0 if unknown.
func parseJavacMajor(out string) int {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "javac ") {
			continue
		}
		parts := strings.FieldsFunc(strings.TrimPrefix(line, "javac "), func(r rune) bool {
			return r == '.' || r == '-' || r == '_' || r == '+'
		})
		if len(parts) == 0 {
			return 0
		}
		n, _ := strconv.Atoi(parts[0])
		if n == 1 && len(parts) > 1 {
			n, _ = strconv.Atoi(parts[1])
		}
		return n
	}
	return 0
}

var (
	javacMajorOnce sync.Once
	javacMajorVal  int
)

func javacMajor() int {
	javacMajorOnce.Do(func() {
		if out, err := exec.Command(javacBinary(), "-version").CombinedOutput(); err == nil {
			javacMajorVal = parseJavacMajor(string(out))
		}
	})
	return javacMajorVal
}
