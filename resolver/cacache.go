// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

type CACache struct {
	Dir string
}

func (c *CACache) Put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(c.Dir, key)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return "", err
		}
	}
	return key, nil
}

func (c *CACache) Get(key string) ([]byte, error) {
	target := filepath.Join(c.Dir, key)
	data, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

func (c *CACache) Link(key, dest string) error {
	src := filepath.Join(c.Dir, key)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dest)
	if err := os.Link(src, dest); err == nil {
		return nil
	}
	// Fallback to copy if hard link fails across filesystems
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}
