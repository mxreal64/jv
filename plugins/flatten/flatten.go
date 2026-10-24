// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package flatten

import (
	"bytes"
	"os"
	"regexp"

	"github.com/mxreal64/jv/compiler"
)

type FlattenMode string

const (
	ModeResolveCiFriendlyVersions FlattenMode = "resolveCiFriendlyVersions"
	ModeOssrh                     FlattenMode = "ossrh"
	ModeBom                       FlattenMode = "bom"
	ModeDefaults                  FlattenMode = "defaults"
)

var (
	buildTagRegex      = regexp.MustCompile(`(?s)<build>.*?</build>[\r\n]*`)
	profilesTagRegex   = regexp.MustCompile(`(?s)<profiles>.*?</profiles>[\r\n]*`)
	reposTagRegex      = regexp.MustCompile(`(?s)<repositories>.*?</repositories>[\r\n]*`)
	pluginReposRegex   = regexp.MustCompile(`(?s)<pluginRepositories>.*?</pluginRepositories>[\r\n]*`)
	distMgmtRegex      = regexp.MustCompile(`(?s)<distributionManagement>.*?</distributionManagement>[\r\n]*`)
	depMgmtRegex       = regexp.MustCompile(`(?s)<dependencyManagement>.*?</dependencyManagement>[\r\n]*`)
	propertiesTagRegex = regexp.MustCompile(`(?s)<properties>.*?</properties>[\r\n]*`)
)

// Flatten reads pomFile, substitutes properties throughout, and optionally strips sections according to mode.
// Writes output to outFile.
func Flatten(pomFile string, props map[string]string, mode FlattenMode, outFile string) error {
	data, err := os.ReadFile(pomFile)
	if err != nil {
		return err
	}

	if mode == "" {
		mode = ModeResolveCiFriendlyVersions
	}

	// Substitute properties (e.g. ${revision}, ${sha1}, ${changelist}, and general props)
	substituted := compiler.SubstituteProperties(data, props)

	out := substituted
	if mode == ModeOssrh {
		out = buildTagRegex.ReplaceAll(out, nil)
		out = profilesTagRegex.ReplaceAll(out, nil)
		out = reposTagRegex.ReplaceAll(out, nil)
		out = pluginReposRegex.ReplaceAll(out, nil)
		out = distMgmtRegex.ReplaceAll(out, nil)
	} else if mode == ModeDefaults {
		out = buildTagRegex.ReplaceAll(out, nil)
		out = profilesTagRegex.ReplaceAll(out, nil)
		out = reposTagRegex.ReplaceAll(out, nil)
		out = pluginReposRegex.ReplaceAll(out, nil)
		out = distMgmtRegex.ReplaceAll(out, nil)
		out = depMgmtRegex.ReplaceAll(out, nil)
	}

	out = bytes.TrimSpace(out)
	out = append(out, '\n')
	return os.WriteFile(outFile, out, 0o644)
}
