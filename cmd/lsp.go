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
	"strings"
	"sync"
	"time"

	"github.com/mxreal64/jv/bridge"
	"github.com/mxreal64/jv/config"
	"github.com/mxreal64/jv/gradle"
	"github.com/mxreal64/jv/resolver"
)

// LSPRequest is a single JSON-RPC 2.0 request or notification from stdin.
type LSPRequest struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// LSPResponse is a single JSON response line written to stdout.
type LSPResponse struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   interface{}     `json:"error,omitempty"`
}

// lspState is the mutable server state shared between requests.
type lspState struct {
	mu          sync.Mutex
	isGradle    bool
	gradleModel *gradle.Model
	loader      *resolver.Loader
	mods        []*resolver.Module
	arts        map[string][]resolver.Artifact // keyed by module Dir
	mtimes      map[string]time.Time           // build files → last known mtime
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
			_ = enc.Encode(LSPResponse{
				JSONRPC: "2.0",
				Error:   "invalid JSON: " + err.Error(),
			})
			continue
		}

		result, err := st.handle(ctx, root, req)
		resp := LSPResponse{ID: req.ID}
		if req.JSONRPC == "2.0" || len(req.ID) > 0 {
			resp.JSONRPC = "2.0"
		}
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = result
		}
		_ = enc.Encode(resp)
	}
	return scanner.Err()
}

func fileInDir(dir, file string) bool {
	cleanDir := filepath.Clean(dir)
	cleanFile := filepath.Clean(file)
	rel, err := filepath.Rel(cleanDir, cleanFile)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isTestFile(file string, testRoots []string) bool {
	for _, root := range testRoots {
		if fileInDir(root, file) {
			return true
		}
	}
	clean := filepath.ToSlash(filepath.Clean(file))
	return strings.Contains(clean, "/src/test/") ||
		strings.HasSuffix(clean, "Test.java") ||
		strings.HasSuffix(clean, "Tests.java") ||
		strings.HasSuffix(clean, "TestCase.java")
}

// handle dispatches one request. Safe to call from a single goroutine (scanner loop).
func (st *lspState) handle(ctx context.Context, root string, req LSPRequest) (interface{}, error) {
	if err := st.ensureResolved(ctx, root); err != nil {
		return nil, err
	}

	var p struct {
		Scope  string `json:"scope"`
		File   string `json:"file"`
		Module string `json:"module"`
	}
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &p)
	}

	switch req.Method {
	case "classpath":
		if st.isGradle {
			return st.handleGradleClasspath(p.Scope, p.File, p.Module)
		}
		return st.handleMavenClasspath(p.Scope, p.File, p.Module)

	case "compilerOptions":
		if st.isGradle {
			return st.handleGradleCompilerOptions(p.File, p.Module)
		}
		return st.handleMavenCompilerOptions(p.File, p.Module)

	case "sourceRoots":
		if st.isGradle {
			return st.handleGradleSourceRoots(p.File, p.Module)
		}
		return st.handleMavenSourceRoots(p.File, p.Module)

	case "projectInfo":
		if st.isGradle {
			return st.handleGradleProjectInfo()
		}
		return st.handleMavenProjectInfo()

	default:
		return nil, fmt.Errorf("unknown method: %q", req.Method)
	}
}

func (st *lspState) handleMavenClasspath(scope, file, module string) (interface{}, error) {
	if file != "" {
		for _, m := range st.mods {
			if m.Eff == nil {
				continue
			}
			if fileInDir(m.Dir, file) {
				inTest := isTestFile(file, []string{filepath.Join(m.Dir, "src/test/java")})
				if scope == "" {
					if inTest {
						scope = "test"
					} else {
						scope = "compile"
					}
				}
				a := st.arts[m.Dir]
				outDir := filepath.Join(m.Dir, "target/classes")
				if inTest {
					outDir = filepath.Join(m.Dir, "target/test-classes")
				}
				release := m.Eff.Props["maven.compiler.release"]
				if release == "" {
					release = m.Eff.Props["java.version"]
				}
				return map[string]interface{}{
					"classpath": lspDedup(resolver.Classpath(a, scope)),
					"scope":     scope,
					"module":    m.Eff.A,
					"release":   release,
					"outputDir": outDir,
				}, nil
			}
		}
	}

	if module != "" {
		for _, m := range st.mods {
			if m.Eff == nil {
				continue
			}
			if m.Eff.A == module || filepath.Base(m.Dir) == module || m.Dir == module {
				if scope == "" {
					scope = "test"
				}
				a := st.arts[m.Dir]
				return map[string]interface{}{
					"classpath": lspDedup(resolver.Classpath(a, scope)),
					"scope":     scope,
					"module":    m.Eff.A,
				}, nil
			}
		}
	}

	if scope == "" {
		scope = "test"
	}
	var cp []string
	for _, a := range st.arts {
		cp = append(cp, resolver.Classpath(a, scope)...)
	}
	return map[string]interface{}{"classpath": lspDedup(cp)}, nil
}

func (st *lspState) handleGradleClasspath(scope, file, module string) (interface{}, error) {
	if st.gradleModel == nil {
		return map[string]interface{}{"classpath": []string{}}, nil
	}

	if file != "" {
		for _, p := range st.gradleModel.Projects {
			if fileInDir(p.Dir, file) {
				inTest := isTestFile(file, p.TestJava)
				if scope == "" {
					if inTest {
						scope = "test"
					} else {
						scope = "compile"
					}
				}
				list := p.Runtime
				if inTest {
					list = p.TestRuntime
				} else if scope == "compile" {
					list = p.Compile
				}
				outDir := ""
				if inTest && len(p.TestClasses) > 0 {
					outDir = p.TestClasses[0]
				} else if len(p.MainClasses) > 0 {
					outDir = p.MainClasses[0]
				}
				return map[string]interface{}{
					"classpath": lspDedup(st.gradleModel.Subst(list)),
					"scope":     scope,
					"module":    p.Name,
					"release":   p.Release,
					"outputDir": outDir,
				}, nil
			}
		}
	}

	if module != "" {
		for _, p := range st.gradleModel.Projects {
			if p.Name == module || p.Path == module || filepath.Base(p.Dir) == module {
				if scope == "" {
					scope = "test"
				}
				list := p.TestRuntime
				if scope == "compile" {
					list = p.Compile
				} else if scope == "runtime" {
					list = p.Runtime
				}
				return map[string]interface{}{
					"classpath": lspDedup(st.gradleModel.Subst(list)),
					"scope":     scope,
					"module":    p.Name,
				}, nil
			}
		}
	}

	if scope == "" {
		scope = "test"
	}
	var cp []string
	for _, p := range st.gradleModel.Projects {
		list := p.TestRuntime
		if scope == "compile" {
			list = p.Compile
		} else if scope == "runtime" {
			list = p.Runtime
		}
		cp = append(cp, st.gradleModel.Subst(list)...)
	}
	return map[string]interface{}{"classpath": lspDedup(cp)}, nil
}

func (st *lspState) handleMavenCompilerOptions(file, module string) (interface{}, error) {
	for _, m := range st.mods {
		if m.Eff == nil {
			continue
		}
		if (file != "" && fileInDir(m.Dir, file)) || (module != "" && (m.Eff.A == module || filepath.Base(m.Dir) == module)) || (file == "" && module == "") {
			release := m.Eff.Props["maven.compiler.release"]
			if release == "" {
				release = m.Eff.Props["java.version"]
			}
			source := m.Eff.Props["maven.compiler.source"]
			target := m.Eff.Props["maven.compiler.target"]
			encoding := m.Eff.Props["project.build.sourceEncoding"]
			if encoding == "" {
				encoding = "UTF-8"
			}
			return map[string]interface{}{
				"release":      release,
				"source":       source,
				"target":       target,
				"encoding":     encoding,
				"compilerArgs": m.Eff.Build.CompilerProps(),
				"mainOutput":   filepath.Join(m.Dir, "target/classes"),
				"testOutput":   filepath.Join(m.Dir, "target/test-classes"),
			}, nil
		}
	}
	return map[string]interface{}{
		"encoding": "UTF-8",
	}, nil
}

func (st *lspState) handleGradleCompilerOptions(file, module string) (interface{}, error) {
	if st.gradleModel == nil {
		return map[string]interface{}{"encoding": "UTF-8"}, nil
	}
	for _, p := range st.gradleModel.Projects {
		if (file != "" && fileInDir(p.Dir, file)) || (module != "" && (p.Name == module || p.Path == module)) || (file == "" && module == "") {
			mainOut := ""
			testOut := ""
			if len(p.MainClasses) > 0 {
				mainOut = p.MainClasses[0]
			}
			if len(p.TestClasses) > 0 {
				testOut = p.TestClasses[0]
			}
			encoding := p.Encoding
			if encoding == "" {
				encoding = "UTF-8"
			}
			return map[string]interface{}{
				"release":      p.Release,
				"source":       p.Source,
				"target":       p.Target,
				"encoding":     encoding,
				"compilerArgs": p.CompilerArgs,
				"mainOutput":   mainOut,
				"testOutput":   testOut,
			}, nil
		}
	}
	return map[string]interface{}{"encoding": "UTF-8"}, nil
}

func (st *lspState) handleMavenSourceRoots(file, module string) (interface{}, error) {
	var main, test, res, testRes []string
	for _, m := range st.mods {
		if m.Eff == nil {
			continue
		}
		if (file != "" && !fileInDir(m.Dir, file)) || (module != "" && m.Eff.A != module && filepath.Base(m.Dir) != module) {
			continue
		}
		main = append(main, filepath.Join(m.Dir, "src/main/java"))
		test = append(test, filepath.Join(m.Dir, "src/test/java"))
		res = append(res, filepath.Join(m.Dir, "src/main/resources"))
		testRes = append(testRes, filepath.Join(m.Dir, "src/test/resources"))
	}
	return map[string]interface{}{
		"main":          lspDedup(main),
		"test":          lspDedup(test),
		"resources":     lspDedup(res),
		"testResources": lspDedup(testRes),
	}, nil
}

func (st *lspState) handleGradleSourceRoots(file, module string) (interface{}, error) {
	if st.gradleModel == nil {
		return map[string]interface{}{
			"main":          []string{},
			"test":          []string{},
			"resources":     []string{},
			"testResources": []string{},
		}, nil
	}
	var main, test, res, testRes []string
	for _, p := range st.gradleModel.Projects {
		if (file != "" && !fileInDir(p.Dir, file)) || (module != "" && p.Name != module && p.Path != module) {
			continue
		}
		main = append(main, p.MainJava...)
		test = append(test, p.TestJava...)
		res = append(res, p.MainRes...)
		testRes = append(testRes, p.TestRes...)
	}
	return map[string]interface{}{
		"main":          lspDedup(main),
		"test":          lspDedup(test),
		"resources":     lspDedup(res),
		"testResources": lspDedup(testRes),
	}, nil
}

func (st *lspState) handleMavenProjectInfo() (interface{}, error) {
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
		"buildSystem": "maven",
		"group":       rootGroup,
		"artifact":    rootArtifact,
		"version":     rootVersion,
		"modules":     infos,
	}, nil
}

func (st *lspState) handleGradleProjectInfo() (interface{}, error) {
	type modInfo struct {
		Group    string `json:"group"`
		Artifact string `json:"artifact"`
		Version  string `json:"version"`
		Dir      string `json:"dir"`
	}
	var infos []modInfo
	var rootGroup, rootArtifact, rootVersion string
	if st.gradleModel != nil {
		for i, p := range st.gradleModel.Projects {
			if i == 0 {
				rootGroup, rootArtifact, rootVersion = p.Group, p.Name, p.Version
			}
			infos = append(infos, modInfo{
				Group: p.Group, Artifact: p.Name, Version: p.Version, Dir: p.Dir,
			})
		}
	}
	return map[string]interface{}{
		"buildSystem": "gradle",
		"group":       rootGroup,
		"artifact":    rootArtifact,
		"version":     rootVersion,
		"modules":     infos,
	}, nil
}

// ensureResolved resolves modules and artifacts, caching the result. On
// subsequent calls it re-resolves only when build files have changed.
func (st *lspState) ensureResolved(ctx context.Context, root string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	if (st.mods != nil || st.gradleModel != nil) && !st.lspStale() {
		return nil
	}

	hasPom := false
	if _, err := os.Stat(filepath.Join(root, "pom.xml")); err == nil {
		hasPom = true
	}

	isGradle, gradlew := bridge.DetectGradle(root)
	if !hasPom && isGradle {
		st.isGradle = true
		key, err := gradle.CacheKey(root, nil)
		var model *gradle.Model
		var ok bool
		if err == nil {
			model, ok = gradle.LoadCache(root, key)
		}
		if !ok {
			qctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			model, err = gradle.Query(qctx, gradlew, root, nil, false)
			cancel()
			if err != nil {
				return fmt.Errorf("gradle query: %w", err)
			}
			if key != "" {
				_ = gradle.SaveCache(root, key, model)
			}
		}
		st.gradleModel = model
		st.mtimes = map[string]time.Time{}
		for _, name := range []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "gradle.properties"} {
			p := filepath.Join(root, name)
			if fi, err := os.Stat(p); err == nil {
				st.mtimes[p] = fi.ModTime()
			}
		}
		for _, p := range model.Projects {
			for _, name := range []string{"build.gradle", "build.gradle.kts"} {
				path := filepath.Join(p.Dir, name)
				if fi, err := os.Stat(path); err == nil {
					st.mtimes[path] = fi.ModTime()
				}
			}
		}
		return nil
	}

	st.isGradle = false
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
	out := make([]string, 0, len(ss)) // never nil: clients get [] rather than null
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
