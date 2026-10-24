// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mxreal64/jv/resolver"
)

type PluginDescriptor struct {
	ID              string // groupId:artifactId
	Version         string
	Binary          string // resolved path to the executable
	NativeAvailable bool
}

func cacheDir(id, version string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".jv", "plugins", id, version)
}

// Resolve finds or downloads the plugin binary for the current platform.
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

	return nil, fmt.Errorf("plugin %s:%s not found in cache", id, version)
}
