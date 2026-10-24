// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mxreal64/jv/config"
	"github.com/mxreal64/jv/resolver"
)

// LSPRequest is a single JSON-RPC-style request line from stdin.
type LSPRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// LSPResponse is a single JSON response line written to stdout.
type LSPResponse struct {
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// lspState is the mutable server state shared between requests.
type lspState struct {
	mu       sync.Mutex
	loader   *resolver.Loader
	mods     []*resolver.Module
	arts     map[string][]resolver.Artifact // keyed by module Dir
	mtimes   map[string]time.Time           // pom.xml / jv.toml → last known mtime
}

// RunLSP starts the classpath server. It reads one JSON request per line from
// r, writes one JSON response per line to w, and blocks until r is closed or
// ctx is cancelled.
func RunLSP(ctx context.Context, root string, r io.Reader, w io.Writer) error {
	settings, _ := config.LoadSettings("")
	st := &lspState{
		loader: resolver.NewLoader(ctx, settings.RepoDir(), settings),
		mtimes: map[string]time.Time{},
	}
	st.loader.Quiet = true

	enc := json.NewEncoder(w)
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req LSPRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(LSPResponse{Error: "invalid JSON: " + err.Error()})
			continue
		}

		result, err := st.handle(ctx, root, req)
		if err != nil {
			_ = enc.Encode(LSPResponse{Error: err.Error()})
		} else {
			_ = enc.Encode(LSPResponse{Result: result})
		}
	}
	return scanner.Err()
}

// handle dispatches one request. Safe to call from a single goroutine (scanner loop).
func (st *lspState) handle(ctx context.Context, root string, req LSPRequest) (interface{}, error) {
	if err := st.ensureResolved(ctx, root); err != nil {
		return nil, err
	}

	switch req.Method {
	case "classpath":
		var p struct {
			Scope string `json:"scope"`
		}
		_ = json.Unmarshal(req.Params, &p)
		scope := p.Scope
		if scope == "" {
			scope = "test"
		}
		var cp []string
		for _, a := range st.arts {
			cp = append(cp, resolver.Classpath(a, scope)...)
		}
		return map[string]interface{}{"classpath": lspDedup(cp)}, nil

	case "sourceRoots":
		var main, test []string
		for _, m := range st.mods {
			main = append(main, filepath.Join(m.Dir, "src/main/java"))
			test = append(test, filepath.Join(m.Dir, "src/test/java"))
		}
		return map[string]interface{}{"main": main, "test": test}, nil

	case "projectInfo":
		type modInfo struct {
			Group    string `json:"group"`
			Artifact string `json:"artifact"`
			Version  string `json:"version"`
			Dir      string `json:"dir"`
		}
		var infos []modInfo
		var rootGroup, rootArtifact, rootVersion string
		for i, m := range st.mods {
			if m.Eff == nil {
				continue
			}
			if i == 0 {
				rootGroup, rootArtifact, rootVersion = m.Eff.G, m.Eff.A, m.Eff.V
			}
			infos = append(infos, modInfo{
				Group: m.Eff.G, Artifact: m.Eff.A, Version: m.Eff.V, Dir: m.Dir,
			})
		}
		return map[string]interface{}{
			"group": rootGroup, "artifact": rootArtifact, "version": rootVersion,
			"modules": infos,
		}, nil

	default:
		return nil, fmt.Errorf("unknown method: %q", req.Method)
	}
}

// ensureResolved resolves modules and artifacts, caching the result. On
// subsequent calls it re-resolves only when pom.xml or jv.toml has changed.
func (st *lspState) ensureResolved(ctx context.Context, root string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	if st.mods != nil && !st.lspStale() {
		return nil
	}

	mods, err := st.loader.Discover(root)
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}
	st.loader.Register(mods)

	arts := make(map[string][]resolver.Artifact, len(mods))
	for _, m := range mods {
		if m.Eff == nil || m.Eff.Packaging == "pom" {
			continue
		}
		nodes, err := st.loader.Resolve(m.Eff)
		if err != nil {
			continue // best-effort; other modules still usable
		}
		a, err := st.loader.Artifacts(resolver.Flatten(nodes))
		if err != nil {
			continue
		}
		arts[m.Dir] = a
	}

	st.mods = mods
	st.arts = arts
	st.mtimes = map[string]time.Time{}
	for _, m := range mods {
		for _, name := range []string{"pom.xml", "jv.toml"} {
			p := filepath.Join(m.Dir, name)
			if fi, err := os.Stat(p); err == nil {
				st.mtimes[p] = fi.ModTime()
			}
		}
	}
	return nil
}

// lspStale reports whether any watched file has a newer mtime.
// Must be called with st.mu held.
func (st *lspState) lspStale() bool {
	for p, old := range st.mtimes {
		fi, err := os.Stat(p)
		if err != nil || !fi.ModTime().Equal(old) {
			return true
		}
	}
	return false
}

func lspDedup(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := ss[:0]
	for _, s := range ss {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
