// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package resolver

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mxreal64/jv/config"
)

type blobServer struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func (b *blobServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/blob/")
	b.mu.Lock()
	defer b.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		if data, ok := b.blobs[key]; ok {
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	case http.MethodPut:
		data, _ := io.ReadAll(r.Body)
		b.blobs[key] = data
		w.WriteHeader(http.StatusNoContent)
	}
}

func TestDistCacheClient(t *testing.T) {
	store := &blobServer{blobs: map[string][]byte{}}
	srv := httptest.NewServer(store)
	defer srv.Close()
	d := NewDistCache(srv.URL, "")
	ctx := context.Background()
	dir := t.TempDir()

	key := DistKey("DA39A3EE5E6B4B0D3255BFEF95601890AFD80709")
	if key != DistKey("da39a3ee5e6b4b0d3255bfef95601890afd80709") || len(key) != 64 {
		t.Fatalf("DistKey must be a case-insensitive 64-hex digest, got %q", key)
	}

	if ok, err := d.Get(ctx, key, filepath.Join(dir, "miss")); ok || err != nil {
		t.Errorf("miss: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "miss")); err == nil {
		t.Error("a miss must not leave a file behind")
	}

	src := filepath.Join(dir, "src.jar")
	if err := os.WriteFile(src, []byte("jar-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.Put(ctx, key, src); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "sub", "got.jar")
	if ok, err := d.Get(ctx, key, dest); !ok || err != nil {
		t.Fatalf("hit: ok=%v err=%v", ok, err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "jar-bytes" {
		t.Errorf("got %q", got)
	}

	// an unreachable server is a miss for Get and an error for Put, never a panic
	dead := NewDistCache("http://127.0.0.1:1", "")
	if ok, err := dead.Get(ctx, key, filepath.Join(dir, "x")); ok || err != nil {
		t.Errorf("unreachable Get: ok=%v err=%v", ok, err)
	}
	if err := dead.Put(ctx, key, src); err == nil {
		t.Error("unreachable Put should report an error")
	}
}

func TestLoaderUsesSharedCache(t *testing.T) {
	body := []byte("pretend this is a jar")
	sum := sha1.Sum(body)
	want := hex.EncodeToString(sum[:])

	var jarHits int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".jar.sha1"):
			io.WriteString(w, want)
		case strings.HasSuffix(r.URL.Path, ".jar"):
			atomic.AddInt32(&jarHits, 1)
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()
	store := &blobServer{blobs: map[string][]byte{}}
	cache := httptest.NewServer(store)
	defer cache.Close()

	newLoader := func(withCache bool) *Loader {
		s := &config.Settings{Mirrors: []config.Mirror{{ID: "o", URL: origin.URL, MirrorOf: "*"}}}
		l := NewLoader(context.Background(), t.TempDir(), s)
		l.Quiet = true
		if withCache {
			l.Dist = NewDistCache(cache.URL, "")
		}
		return l
	}
	fetch := func(l *Loader) {
		t.Helper()
		p, err := l.fetchFile("t", "lib", "1.0", "lib-1.0.jar", true)
		if err != nil {
			t.Fatal(err)
		}
		l.FlushDist()
		if got, _ := os.ReadFile(p); string(got) != string(body) {
			t.Fatalf("wrong content %q", got)
		}
	}

	fetch(newLoader(true)) // cold: downloads from the origin and fills the cache
	if atomic.LoadInt32(&jarHits) != 1 {
		t.Fatalf("origin jar hits = %d, want 1", jarHits)
	}
	store.mu.Lock()
	_, shared := store.blobs[DistKey(want)]
	store.mu.Unlock()
	if !shared {
		t.Fatal("verified download was not shared")
	}

	fetch(newLoader(true)) // second machine: served by the cache
	if atomic.LoadInt32(&jarHits) != 1 {
		t.Errorf("second fetch went to the origin (%d jar hits)", jarHits)
	}

	store.mu.Lock()
	store.blobs[DistKey(want)] = []byte("tampered") // a hostile cache must not win
	store.mu.Unlock()
	fetch(newLoader(true))
	if atomic.LoadInt32(&jarHits) != 2 {
		t.Errorf("tampered blob must be rejected and re-downloaded (%d jar hits)", jarHits)
	}

	fetch(newLoader(false)) // no cache configured: plain download
	if atomic.LoadInt32(&jarHits) != 3 {
		t.Errorf("jar hits = %d, want 3", jarHits)
	}
}

func TestSharedCacheDownIsHarmless(t *testing.T) {
	body := []byte("jar")
	sum := sha1.Sum(body)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha1") {
			io.WriteString(w, hex.EncodeToString(sum[:]))
			return
		}
		w.Write(body)
	}))
	defer origin.Close()
	s := &config.Settings{Mirrors: []config.Mirror{{ID: "o", URL: origin.URL, MirrorOf: "*"}}}
	l := NewLoader(context.Background(), t.TempDir(), s)
	l.Quiet = true
	l.Dist = NewDistCache("http://127.0.0.1:1", "")
	p, err := l.fetchFile("t", "lib", "1.0", "lib-1.0.jar", true)
	l.FlushDist()
	if err != nil {
		t.Fatalf("an unreachable cache must not break the build: %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != "jar" {
		t.Errorf("got %q", got)
	}
}
