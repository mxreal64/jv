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

type Workspace struct {
	Modules []string
}

// LoadWorkspace reads jv-workspace.toml from dir.
func LoadWorkspace(dir string) (*Workspace, error) {
	p := filepath.Join(dir, "jv-workspace.toml")
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	ws := &Workspace{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "modules") && strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			val := strings.Trim(strings.TrimSpace(parts[1]), "[] \t")
			for _, m := range strings.Split(val, ",") {
				mod := strings.Trim(strings.TrimSpace(m), "\"'\r\n")
				if mod != "" {
					ws.Modules = append(ws.Modules, mod)
				}
			}
		}
	}
	return ws, nil
}
