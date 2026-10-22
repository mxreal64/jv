// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

// Package jdk locates the Java tools jv shells out to and reports the JDK version.
package jdk

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Tool returns the path of a JDK tool (java, javac, ...). JAVA_HOME wins over PATH,
// matching what Maven and Gradle do.
func Tool(name string) string {
	if home := os.Getenv("JAVA_HOME"); home != "" {
		exe := name
		if runtime.GOOS == "windows" {
			exe += ".exe"
		}
		p := filepath.Join(home, "bin", exe)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return name
}

var (
	once    sync.Once
	version string
)

var verRe = regexp.MustCompile(`version "([^"]+)"`)

// Version returns the running JDK's version string (for example "17.0.2" or "1.8.0_292"),
// or "" if it cannot be determined. The result is cached.
func Version() string {
	once.Do(func() { version = detect() })
	return version
}

func detect() string {
	if home := os.Getenv("JAVA_HOME"); home != "" {
		if f, err := os.Open(filepath.Join(home, "release")); err == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				line := sc.Text()
				if strings.HasPrefix(line, "JAVA_VERSION=") {
					return strings.Trim(strings.TrimPrefix(line, "JAVA_VERSION="), `"' `)
				}
			}
		}
	}
	out, err := exec.Command(Tool("java"), "-version").CombinedOutput()
	if err != nil {
		return ""
	}
	if m := verRe.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

// Major converts a version string to its feature release: "1.8.0_292" -> 8, "17.0.2" -> 17.
func Major(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '_' || r == '-' || r == '+' })
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	if n == 1 && len(parts) > 1 {
		if m, err := strconv.Atoi(parts[1]); err == nil {
			return m
		}
	}
	return n
}
