// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mxreal64/jv/resolver"
)

type PluginDescriptor struct {
	ID              string // groupId:artifactId
	Version         string
	Binary          string // resolved path to the executable
	NativeAvailable bool
}

// Root is the directory plugins are installed in: $JV_PLUGIN_DIR, or ~/.jv/plugins.
func Root() string {
	if d := os.Getenv("JV_PLUGIN_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".jv", "plugins")
}

func cacheDir(id, version string) string {
	cleanID := strings.ReplaceAll(id, ":", string(filepath.Separator))
	return filepath.Join(Root(), cleanID, version)
}

// BinaryPath is where the executable of plugin id:version is expected.
func BinaryPath(id, version string) string {
	name := "plugin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(cacheDir(id, version), name)
}

// Resolve finds the installed plugin binary for the current platform. jv does not download plugins:
// install the executable at BinaryPath(id, version) yourself.
func Resolve(ctx context.Context, id, version string, loader *resolver.Loader) (*PluginDescriptor, error) {
	cd := cacheDir(id, version)
	binName := "plugin"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	cachedBin := filepath.Join(cd, binName)
	if fi, err := os.Stat(cachedBin); err == nil && !fi.IsDir() {
		return &PluginDescriptor{
			ID:              id,
			Version:         version,
			Binary:          cachedBin,
			NativeAvailable: true,
		}, nil
	}

	return nil, fmt.Errorf("plugin %s:%s not found in cache: it is not installed at %s", id, version, cachedBin)
}
