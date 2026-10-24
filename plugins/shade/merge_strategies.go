// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package shade

import (
	"bytes"
	"strings"
)

type MergeStrategy interface {
	Matches(path string) bool
	Transform(path string, existing []byte, incoming []byte) ([]byte, error)
}

// AppendingTransformer concatenates matching files (like META-INF/services/*)
type AppendingTransformer struct {
	PathPrefix string
}

func (a *AppendingTransformer) Matches(path string) bool {
	if a.PathPrefix == "" {
		return strings.HasPrefix(path, "META-INF/services/")
	}
	return strings.HasPrefix(path, a.PathPrefix)
}

func (a *AppendingTransformer) Transform(path string, existing []byte, incoming []byte) ([]byte, error) {
	if len(existing) == 0 {
		return incoming, nil
	}
	var buf bytes.Buffer
	buf.Write(bytes.TrimRight(existing, "\r\n"))
	buf.WriteString("\n")
	buf.Write(bytes.TrimLeft(incoming, "\r\n"))
	return buf.Bytes(), nil
}

// ServicesResourceTransformer merges service provider files by appending distinct lines
type ServicesResourceTransformer struct{}

func (s *ServicesResourceTransformer) Matches(path string) bool {
	return strings.HasPrefix(path, "META-INF/services/")
}

func (s *ServicesResourceTransformer) Transform(path string, existing []byte, incoming []byte) ([]byte, error) {
	seen := map[string]bool{}
	var lines []string

	for _, data := range [][]byte{existing, incoming} {
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				if !seen[trimmed] {
					seen[trimmed] = true
					lines = append(lines, trimmed)
				}
			}
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}
