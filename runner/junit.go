// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// ErrTestsFailed means the JUnit launcher ran and reported failing tests.
var ErrTestsFailed = errors.New("one or more tests failed")

func javaBinary() string {
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		p := filepath.Join(jh, "bin", "java")
		if runtime.GOOS == "windows" {
			p += ".exe"
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return "java"
}

// RunTests runs the JUnit Platform console launcher over target/test-classes.
// launcher is an extra jar to prepend (empty if the project already provides it).
// testFilter can be passed to execute specific tests (e.g. from -Dtest=...).
func RunTests(ctx context.Context, launcher string, classpath []string, testFilter ...string) error {
	return RunTestsIn(ctx, Config{}, launcher, classpath, testFilter...)
}

// Config customises where tests live and how the test JVM is started. The zero value is the
// Maven layout.
type Config struct {
	Scan     string            // directory of compiled test classes (default target/test-classes)
	Reports  string            // JUnit XML reports directory (default target/surefire-reports)
	JVMArgs  []string          // extra JVM arguments (e.g. from a Gradle test task or argLine)
	SysProps map[string]string // -Dkey=value for the test JVM
	// Surefire-derived filters. When Includes is nil the four Surefire defaults apply.
	Includes       []string // --include-classname regexes
	Excludes       []string // --exclude-classname regexes
	Groups         string   // comma-separated JUnit tag expression → --include-tag
	ExcludedGroups string   // comma-separated JUnit tag expression → --exclude-tag
}

// RunTestsIn is RunTests with an explicit layout.
func RunTestsIn(ctx context.Context, cfg Config, launcher string, classpath []string, testFilter ...string) error {
	scan, reports := cfg.Scan, cfg.Reports
	if scan == "" {
		scan = "target/test-classes"
	}
	if reports == "" {
		reports = "target/surefire-reports"
	}
	if st, err := os.Stat(scan); err != nil || !st.IsDir() {
		fmt.Fprintln(os.Stderr, "[jv] No tests found.")
		return nil
	}
	cp := classpath
	if launcher != "" {
		cp = append([]string{launcher}, classpath...)
	}
	fmt.Fprintln(os.Stderr, "[jv] Executing JUnit Platform tests...")

	args := append([]string{}, cfg.JVMArgs...)
	keys := make([]string, 0, len(cfg.SysProps))
	for k := range cfg.SysProps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-D"+k+"="+cfg.SysProps[k])
	}
	args = append(args,
		"-cp", strings.Join(cp, string(os.PathListSeparator)),
		"org.junit.platform.console.ConsoleLauncher",
		"execute",
		"--reports-dir", reports,
	)

	if cfg.Groups != "" {
		for _, g := range strings.Split(cfg.Groups, ",") {
			if t := strings.TrimSpace(g); t != "" {
				args = append(args, "--include-tag", t)
			}
		}
	}
	if cfg.ExcludedGroups != "" {
		for _, g := range strings.Split(cfg.ExcludedGroups, ",") {
			if t := strings.TrimSpace(g); t != "" {
				args = append(args, "--exclude-tag", t)
			}
		}
	}

	filter := ""
	if len(testFilter) > 0 {
		filter = strings.TrimSpace(testFilter[0])
	}

	// Split -Dtest into method selectors (Class#method) and class filters.
	var methods, classes []string
	needScan := false
	for _, f := range strings.Split(filter, ",") {
		f = strings.TrimSpace(f)
		switch {
		case f == "":
		case strings.Contains(f, "#"):
			methods = append(methods, f)
		default:
			classes = append(classes, f)
			// wildcards and simple names can't be selected directly, only matched by scanning
			if strings.Contains(f, "*") || !strings.Contains(f, ".") {
				needScan = true
			}
		}
	}
	for _, m := range methods {
		args = append(args, "--select-method", m)
	}
	switch {
	case needScan:
		// --include-classname only filters; the launcher still needs a selector to scan.
		for _, c := range classes {
			args = append(args, "--include-classname", classRegex(c))
		}
		args = append(args, "--scan-classpath", scan)
	case len(classes) > 0:
		for _, c := range classes {
			args = append(args, "--select-class", c)
		}
	case len(methods) == 0:
		// Plain scan: apply the Surefire include/exclude patterns. (-Dtest, handled above,
		// overrides them, as in Surefire.)
		for _, inc := range cfg.Includes {
			args = append(args, "--include-classname", inc)
		}
		for _, exc := range cfg.Excludes {
			args = append(args, "--exclude-classname", exc)
		}
		if len(cfg.Includes) > 0 { // Maven layout: nested and anonymous classes are not test classes
			args = append(args, "--exclude-classname", `.*\$.*`)
		}
		args = append(args, "--scan-classpath", scan)
	}

	cmd := exec.CommandContext(ctx, javaBinary(), args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return ErrTestsFailed
		}
		return err
	}
	return nil
}

// classRegex turns a Maven -Dtest class pattern (Foo, Foo*, *IT, com.acme.Foo*) into a
// regex that fully matches fully qualified class names, as --include-classname expects.
func classRegex(f string) string {
	parts := strings.Split(f, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	re := strings.Join(parts, ".*")
	if !strings.Contains(f, ".") { // simple name: match in any package
		re = `(?:.*\.)?` + re
	}
	return re
}

// AntGlobToClassRegex converts a Surefire Ant glob such as **/*IT.java or com/acme/*Test to a
// regular expression that fully matches fully-qualified class names, as
// --include-classname / --exclude-classname expect. "**/" spans any number of packages, "*" and
// "?" stay inside one name segment, and a pattern without "**/" is anchored at the default
// package, exactly like Surefire's file-based matching.
func AntGlobToClassRegex(glob string) string {
	g := strings.TrimSpace(glob)
	g = strings.TrimSuffix(strings.TrimSuffix(g, ".java"), ".class")
	var sb strings.Builder
	for i := 0; i < len(g); i++ {
		switch c := g[i]; c {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				i++
				if i+1 < len(g) && g[i+1] == '/' {
					i++
					sb.WriteString(`(?:[^.]+\.)*`)
				} else {
					sb.WriteString(`.*`)
				}
			} else {
				sb.WriteString(`[^.]*`)
			}
		case '?':
			sb.WriteString(`[^.]`)
		case '/', '.':
			sb.WriteString(`\.`)
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return sb.String()
}
