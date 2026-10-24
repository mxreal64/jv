// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	dir := flag.String("dir", "/tmp/jv-cache", "storage directory")
	addr := flag.String("addr", ":8080", "bind address")
	token := flag.String("token", "", "optional auth token")
	flag.Parse()

	_ = os.MkdirAll(*dir, 0o755)

	http.HandleFunc("/blob/", func(w http.ResponseWriter, r *http.Request) {
		if *token != "" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+*token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		key := strings.TrimPrefix(r.URL.Path, "/blob/")
		if key == "" || strings.Contains(key, "/") || strings.Contains(key, "..") {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		target := filepath.Join(*dir, key)

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
			f, err := os.Create(target)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer f.Close()
			_, _ = io.Copy(f, r.Body)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	fmt.Printf("jv-cache listening on %s\n", *addr)
	_ = http.ListenAndServe(*addr, nil)
}
