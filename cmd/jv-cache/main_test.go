// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func do(t *testing.T, method, url, token, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestHandlerRoundTripAndAuth(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(handler(dir, "s3cret"))
	defer srv.Close()
	key := strings.Repeat("ab", 32)
	u := srv.URL + "/blob/" + key

	if code, _ := do(t, "GET", u, "", ""); code != http.StatusUnauthorized {
		t.Errorf("no token: got %d, want 401", code)
	}
	if code, _ := do(t, "GET", u, "wrong", ""); code != http.StatusUnauthorized {
		t.Errorf("wrong token: got %d, want 401", code)
	}
	if code, _ := do(t, "GET", u, "s3cret", ""); code != http.StatusNotFound {
		t.Errorf("missing blob: got %d, want 404", code)
	}
	if code, _ := do(t, "PUT", u, "s3cret", "payload"); code != http.StatusNoContent {
		t.Fatalf("put: got %d", code)
	}
	if code, body := do(t, "GET", u, "s3cret", ""); code != http.StatusOK || body != "payload" {
		t.Errorf("get: %d %q", code, body)
	}
	if code, _ := do(t, "HEAD", u, "s3cret", ""); code != http.StatusOK {
		t.Errorf("head: %d", code)
	}
	if code, _ := do(t, "GET", srv.URL+"/blob/../etc/passwd", "s3cret", ""); code == http.StatusOK {
		t.Error("path traversal must not succeed")
	}
	if code, _ := do(t, "PUT", srv.URL+"/blob/not-a-key", "s3cret", "x"); code != http.StatusBadRequest {
		t.Errorf("bad key: got %d, want 400", code)
	}
	if code, _ := do(t, "DELETE", u, "s3cret", ""); code != http.StatusMethodNotAllowed {
		t.Errorf("delete: got %d, want 405", code)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("only the blob should remain in the store, found %d entries", len(ents))
	}
}

func TestOpenServerNeedsNoToken(t *testing.T) {
	srv := httptest.NewServer(handler(t.TempDir(), ""))
	defer srv.Close()
	if code, _ := do(t, "PUT", srv.URL+"/blob/"+strings.Repeat("0f", 32), "", "x"); code != http.StatusNoContent {
		t.Errorf("open server: got %d", code)
	}
}
