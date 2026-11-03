// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DistCache is the client of a jv-cache server (cmd/jv-cache), a build cache shared by a team or by
// CI jobs. Blobs are addressed by DistKey.
type DistCache struct {
	URL    string
	Token  string
	client *http.Client
}

func NewDistCache(url, token string) *DistCache {
	return &DistCache{
		URL:    strings.TrimRight(url, "/"),
		Token:  token,
		client: &http.Client{Timeout: 2 * time.Minute},
	}
}

// DistKey is the cache key of an artifact: the SHA-256 of the SHA-1 checksum its origin repository
// publishes. Equal artifacts share a key, and a blob fetched under a key is only accepted when its
// own SHA-1 matches that checksum, so a cache server can never substitute different content.
func DistKey(sha1hex string) string {
	sum := sha256.Sum256([]byte("sha1:" + strings.ToLower(strings.TrimSpace(sha1hex))))
	return hex.EncodeToString(sum[:])
}

func fileSHA1(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Get downloads the blob stored under key to dest. A missing blob or an unreachable server is a
// miss (false, nil); only local file errors are reported. dest is removed on failure.
func (d *DistCache) Get(ctx context.Context, key string, dest string) (bool, error) {
	if d == nil || d.URL == "" {
		return false, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", d.URL+"/blob/"+key, nil)
	if err != nil {
		return false, err
	}
	if d.Token != "" {
		req.Header.Set("Authorization", "Bearer "+d.Token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return false, nil // cache unreachable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}
	f, err := os.Create(dest)
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(dest)
		return false, nil // truncated transfer: treat as a miss
	}
	if err := f.Close(); err != nil {
		os.Remove(dest)
		return false, err
	}
	return true, nil
}

// Put uploads the file at src under key.
func (d *DistCache) Put(ctx context.Context, key string, src string) error {
	if d == nil || d.URL == "" {
		return nil
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", d.URL+"/blob/"+key, f)
	if err != nil {
		return err
	}
	req.ContentLength = st.Size()
	if d.Token != "" {
		req.Header.Set("Authorization", "Bearer "+d.Token)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("cache upload: HTTP %d", resp.StatusCode)
	}
	return nil
}

// InsecureCacheURL reports whether url is plain http to a host other than localhost.
func InsecureCacheURL(url string) bool { return insecureRemote(url) }
