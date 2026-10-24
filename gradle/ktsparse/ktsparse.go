// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package ktsparse

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var ErrUnsupported = errors.New("unsupported Kotlin DSL construct")

type PluginRef struct {
	ID      string
	Version string
}

type Repository struct {
	URL string
}

type KTSDep struct {
	Config      string // "implementation", "testImplementation", "api", etc.
	Coordinates string // "group:artifact:version" or project(":name")
}

type KTSModel struct {
	Plugins      []PluginRef
	Group        string
	Version      string
	Repositories []Repository
	Dependencies []KTSDep
	SourceCompat string
	MainClass    string
	SubProjects  []string // from settings.gradle.kts include(":")
}

var (
	depRegex    = regexp.MustCompile(`(implementation|testImplementation|api|runtimeOnly|compileOnly)\s*\(\s*["']([^"']+)["']\s*\)`)
	projRegex   = regexp.MustCompile(`(implementation|testImplementation|api)\s*\(\s*project\s*\(\s*["']([^"']+)["']\s*\)\s*\)`)
	pluginRegex = regexp.MustCompile(`id\s*\(\s*["']([^"']+)["']\s*\)(\s*version\s*["']([^"']+)["'])?`)
)

// Parse parses a supported subset of build.gradle.kts statically.
func Parse(file string) (*KTSModel, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	str := string(data)
	if strings.Contains(str, "buildscript {") || strings.Contains(str, "buildscript{") {
		return nil, fmt.Errorf("%w: buildscript block", ErrUnsupported)
	}
	if strings.Contains(str, "tasks.register") || strings.Contains(str, "task(") || strings.Contains(str, "tasks.create") {
		return nil, fmt.Errorf("%w: custom task definition", ErrUnsupported)
	}

	model := &KTSModel{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	currentBlock := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") {
			continue
		}

		if strings.HasSuffix(line, "{") {
			blockName := strings.TrimSpace(strings.TrimSuffix(line, "{"))
			currentBlock = blockName
			continue
		}
		if line == "}" {
			currentBlock = ""
			continue
		}

		// Simple properties
		if strings.HasPrefix(line, "group =") {
			model.Group = strings.Trim(strings.TrimPrefix(line, "group ="), " \"'\t")
		}
		if strings.HasPrefix(line, "version =") {
			model.Version = strings.Trim(strings.TrimPrefix(line, "version ="), " \"'\t")
		}

		switch currentBlock {
		case "plugins":
			if line == "java" || line == "application" || line == "`java-library`" {
				model.Plugins = append(model.Plugins, PluginRef{ID: strings.Trim(line, "`")})
			} else if m := pluginRegex.FindStringSubmatch(line); len(m) > 1 {
				ver := ""
				if len(m) > 3 {
					ver = m[3]
				}
				model.Plugins = append(model.Plugins, PluginRef{ID: m[1], Version: ver})
			}
		case "dependencies":
			if m := depRegex.FindStringSubmatch(line); len(m) > 2 {
				model.Dependencies = append(model.Dependencies, KTSDep{Config: m[1], Coordinates: m[2]})
			} else if m := projRegex.FindStringSubmatch(line); len(m) > 2 {
				model.Dependencies = append(model.Dependencies, KTSDep{Config: m[1], Coordinates: "project(" + m[2] + ")"})
			}
		case "repositories":
			if strings.Contains(line, "mavenCentral()") {
				model.Repositories = append(model.Repositories, Repository{URL: "https://repo.maven.apache.org/maven2"})
			}
		}
	}

	return model, nil
}
