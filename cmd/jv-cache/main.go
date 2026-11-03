// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"crypto/subtle"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var keyRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// maxBlob bounds a single upload.
const maxBlob = 1 << 30

// handler serves the blob store of dir; token, when not empty, must be sent as a bearer token.
func handler(dir, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/blob/", func(w http.ResponseWriter, r *http.Request) {
		if token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		key := r.URL.Path[len("/blob/"):]
		if !keyRE.MatchString(key) {
			http.Error(w, "bad request: key must be 64 hex characters", http.StatusBadRequest)
			return
		}
		target := filepath.Join(dir, key)

		switch r.Method {
		case http.MethodGet:
			http.ServeFile(w, r, target)
		case http.MethodHead:
			if _, err := os.Stat(target); os.IsNotExist(err) {
				w.WriteHeader(http.StatusNotFound)
			} else {
				w.WriteHeader(http.StatusOK)
			}
		case http.MethodPut:
			// Write to a temporary file and rename, so readers never see a partial blob.
			tmp, err := os.CreateTemp(dir, ".upload-*")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer os.Remove(tmp.Name())
			if _, err := io.Copy(tmp, http.MaxBytesReader(w, r.Body, maxBlob)); err != nil {
				tmp.Close()
				http.Error(w, "upload failed: "+err.Error(), http.StatusBadRequest)
				return
			}
			if err := tmp.Close(); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := os.Rename(tmp.Name(), target); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func main() {
	dir := flag.String("dir", "/tmp/jv-cache", "storage directory")
	addr := flag.String("addr", ":8080", "bind address")
	token := flag.String("token", "", "optional auth token")
	flag.Parse()

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "jv-cache:", err)
		os.Exit(1)
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler(*dir, *token),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	fmt.Printf("jv-cache listening on %s\n", *addr)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "jv-cache:", err)
		os.Exit(1)
	}
}
