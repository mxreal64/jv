// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package shade

import (
	"path/filepath"
	"strings"
)

type Filter struct {
	Artifact string   // groupId:artifactId or *:*
	Includes []string // Ant globs on entry paths within the jar
	Excludes []string
}

// Matches returns true if artifact matches the filter specification (e.g. "group:artifact" or "*:*")
func (f *Filter) MatchesArtifact(group, artifact string) bool {
	if f.Artifact == "" || f.Artifact == "*:*" || f.Artifact == "*" {
		return true
	}
	parts := strings.Split(f.Artifact, ":")
	if len(parts) == 1 {
		return parts[0] == "*" || parts[0] == artifact
	}
	gMatch := parts[0] == "*" || parts[0] == group
	aMatch := parts[1] == "*" || parts[1] == artifact
	return gMatch && aMatch
}

// AllowsEntry checks if entryPath inside the jar is accepted by Includes and Excludes.
func (f *Filter) AllowsEntry(entryPath string) bool {
	// If Includes specified, must match at least one include
	if len(f.Includes) > 0 {
		matched := false
		for _, inc := range f.Includes {
			if globMatch(inc, entryPath) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// If Excludes specified, must not match any exclude
	for _, exc := range f.Excludes {
		if globMatch(exc, entryPath) {
			return false
		}
	}

	return true
}

func globMatch(pattern, path string) bool {
	pattern = strings.TrimPrefix(pattern, "/")
	path = strings.TrimPrefix(path, "/")
	// Convert ant glob ** and * to simple matchers or use filepath.Match
	if strings.Contains(pattern, "**") {
		parts := strings.Split(pattern, "**")
		if len(parts) == 2 {
			prefix := strings.TrimSuffix(parts[0], "/")
			suffix := strings.TrimPrefix(parts[1], "/")
			if prefix != "" && !strings.HasPrefix(path, prefix) {
				return false
			}
			if suffix != "" && !strings.HasSuffix(path, suffix) {
				return false
			}
			return true
		}
	}
	matched, _ := filepath.Match(pattern, path)
	if matched {
		return true
	}
	// Also test filename only if pattern has no slash
	if !strings.Contains(pattern, "/") {
		matched, _ = filepath.Match(pattern, filepath.Base(path))
	}
	return matched
}
