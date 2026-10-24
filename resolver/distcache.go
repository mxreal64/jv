// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type DistCache struct {
	URL    string
	Token  string
	client *http.Client
}

func NewDistCache(url, token string) *DistCache {
	return &DistCache{
		URL:   url,
		Token: token,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (d *DistCache) Get(ctx context.Context, sha256 string, dest string) (bool, error) {
	if d.URL == "" {
		return false, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/blob/%s", d.URL, sha256), nil)
	if err != nil {
		return false, err
	}
	if d.Token != "" {
		req.Header.Set("Authorization", "Bearer "+d.Token)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return false, nil // Cache unreachable
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return false, err
		}
		f, err := os.Create(dest)
		if err != nil {
			return false, err
		}
		defer f.Close()
		_, err = io.Copy(f, resp.Body)
		return err == nil, err
	}
	return false, nil
}

func (d *DistCache) Put(ctx context.Context, sha256 string, src string) error {
	if d.URL == "" {
		return nil
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	req, err := http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("%s/blob/%s", d.URL, sha256), f)
	if err != nil {
		return err
	}
	if d.Token != "" {
		req.Header.Set("Authorization", "Bearer "+d.Token)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil // Non-fatal upload error
	}
	_ = resp.Body.Close()
	return nil
}
