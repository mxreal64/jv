// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package model

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

type JVToml struct {
	Build      BuildConfig     `toml:"build"`
	Test       TestConfig      `toml:"test"`
	Plugins    []PluginDecl    `toml:"plugin"`
	Generators []GeneratorDecl `toml:"generator"`
}

type BuildConfig struct {
	Source   string `toml:"source"`
	Release  string `toml:"release"`
	Encoding string `toml:"encoding"`
}

type TestConfig struct {
	Includes       []string `toml:"includes"`
	Excludes       []string `toml:"excludes"`
	Groups         string   `toml:"groups"`
	ExcludedGroups string   `toml:"excludedGroups"`
	ArgLine        string   `toml:"argLine"`
}

type PluginDecl struct {
	ID      string                 `toml:"id"`
	Version string                 `toml:"version"`
	Phase   string                 `toml:"phase"`
	Config  map[string]interface{} `toml:"config"`
}

type GeneratorDecl struct {
	ID      string   `toml:"id"`
	Phase   string   `toml:"phase"`
	Command []string `toml:"command"`
	Output  string   `toml:"output"`
	Inputs  []string `toml:"inputs"`
}

// Load reads jv.toml from dir. Returns zero value if the file doesn't exist.
// Implements a clean dependency-free TOML parser for jv.toml subset.
func Load(dir string) (JVToml, error) {
	p := filepath.Join(dir, "jv.toml")
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return JVToml{}, nil
	}
	if err != nil {
		return JVToml{}, err
	}

	var res JVToml
	res.PluginDeclInit()

	scanner := bufio.NewScanner(bytes.NewReader(data))
	currentSection := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sec := strings.Trim(line, "[]")
			currentSection = sec
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), "\"'\r\n")

		switch currentSection {
		case "build":
			switch key {
			case "source":
				res.Build.Source = val
			case "release":
				res.Build.Release = val
			case "encoding":
				res.Build.Encoding = val
			}
		case "test":
			switch key {
			case "groups":
				res.Test.Groups = val
			case "excludedGroups":
				res.Test.ExcludedGroups = val
			case "argLine":
				res.Test.ArgLine = val
			}
		}
	}

	return res, nil
}

func (j *JVToml) PluginDeclInit() {
	if j.Plugins == nil {
		j.Plugins = []PluginDecl{}
	}
	if j.Generators == nil {
		j.Generators = []GeneratorDecl{}
	}
}
