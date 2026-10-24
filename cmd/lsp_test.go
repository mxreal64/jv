// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mxreal64/jv/cmd"
)

// minimalPOM is a self-contained POM with no external dependencies so the
// test runs offline without hitting Maven Central.
const minimalPOM = `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
         xsi:schemaLocation="http://maven.apache.org/POM/4.0.0
         http://maven.apache.org/xsd/maven-4.0.0.xsd">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>lsp-test</artifactId>
  <version>1.0-SNAPSHOT</version>
</project>`

func writePOM(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "pom.xml")
	if err := os.WriteFile(p, []byte(minimalPOM), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func sendRequest(t *testing.T, in *bytes.Buffer, method string, params interface{}) {
	t.Helper()
	req := map[string]interface{}{"method": method}
	if params != nil {
		req["params"] = params
	} else {
		req["params"] = map[string]interface{}{}
	}
	b, _ := json.Marshal(req)
	in.Write(b)
	in.WriteByte('\n')
}

func readResponse(t *testing.T, out *bytes.Buffer) map[string]interface{} {
	t.Helper()
	dec := json.NewDecoder(out)
	var resp map[string]interface{}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// runLSP runs RunLSP with a deadline, feeding input and capturing output.
// It returns the stdout bytes.
func runLSP(t *testing.T, dir string, reqs func(in *bytes.Buffer)) []byte {
	t.Helper()

	var in, out bytes.Buffer
	reqs(&in)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// RunLSP blocks until input is drained; wrap in a goroutine.
	done := make(chan error, 1)
	go func() { done <- cmd.RunLSP(ctx, dir, &in, &out) }()

	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("RunLSP: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("RunLSP timed out")
	}
	return out.Bytes()
}

// TestLSPProjectInfo checks that projectInfo returns the correct GAV.
func TestLSPProjectInfo(t *testing.T) {
	dir := t.TempDir()
	writePOM(t, dir)

	outBytes := runLSP(t, dir, func(in *bytes.Buffer) {
		sendRequest(t, in, "projectInfo", nil)
	})

	dec := json.NewDecoder(bytes.NewReader(outBytes))
	var resp struct {
		Result struct {
			Group    string `json:"group"`
			Artifact string `json:"artifact"`
			Version  string `json:"version"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Result.Group != "com.example" {
		t.Errorf("group = %q, want %q", resp.Result.Group, "com.example")
	}
	if resp.Result.Artifact != "lsp-test" {
		t.Errorf("artifact = %q, want %q", resp.Result.Artifact, "lsp-test")
	}
	if resp.Result.Version != "1.0-SNAPSHOT" {
		t.Errorf("version = %q, want %q", resp.Result.Version, "1.0-SNAPSHOT")
	}
}

// TestLSPSourceRoots checks that sourceRoots returns src/main/java and src/test/java.
func TestLSPSourceRoots(t *testing.T) {
	dir := t.TempDir()
	writePOM(t, dir)

	outBytes := runLSP(t, dir, func(in *bytes.Buffer) {
		sendRequest(t, in, "sourceRoots", nil)
	})

	dec := json.NewDecoder(bytes.NewReader(outBytes))
	var resp struct {
		Result struct {
			Main []string `json:"main"`
			Test []string `json:"test"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if len(resp.Result.Main) == 0 {
		t.Error("expected at least one main source root")
	}
	for _, p := range resp.Result.Main {
		if !strings.HasSuffix(p, "src/main/java") {
			t.Errorf("unexpected main root: %s", p)
		}
	}
	for _, p := range resp.Result.Test {
		if !strings.HasSuffix(p, "src/test/java") {
			t.Errorf("unexpected test root: %s", p)
		}
	}
}

// TestLSPClasspathNoDeps checks that classpath returns no error for a project with no deps.
func TestLSPClasspathNoDeps(t *testing.T) {
	dir := t.TempDir()
	writePOM(t, dir)

	outBytes := runLSP(t, dir, func(in *bytes.Buffer) {
		sendRequest(t, in, "classpath", map[string]string{"scope": "test"})
	})

	dec := json.NewDecoder(bytes.NewReader(outBytes))
	var resp struct {
		Result struct {
			Classpath []string `json:"classpath"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	// No deps declared → empty (or only module-local) classpath; at minimum no crash.
}

// TestLSPCacheInvalidation checks that modifying pom.xml causes the cache to be
// treated as stale on the next request.
func TestLSPCacheInvalidation(t *testing.T) {
	dir := t.TempDir()
	pomPath := writePOM(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use a pipe so we can interleave writes with reads.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- cmd.RunLSP(ctx, dir, pr, &out) }()

	writeReq := func(method string) {
		var buf bytes.Buffer
		sendRequest(t, &buf, method, nil)
		_, _ = pw.Write(buf.Bytes())
	}
	readResp := func() map[string]interface{} {
		// Poll until a full JSON line is available.
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
			snap := out.Bytes()
			dec := json.NewDecoder(bytes.NewReader(snap))
			var v map[string]interface{}
			if dec.Decode(&v) == nil {
				// consume exactly the bytes decoded
				out.Next(int(dec.InputOffset()))
				return v
			}
		}
		t.Fatal("timed out waiting for LSP response")
		return nil
	}

	// First request — resolves against original pom.xml.
	writeReq("projectInfo")
	first := readResp()

	// Modify pom.xml with a clearly future mtime so lspStale() fires.
	modified := strings.Replace(minimalPOM, "lsp-test", "lsp-test-modified", 1)
	if err := os.WriteFile(pomPath, []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(pomPath, future, future)

	// Second request — cache must be invalidated.
	writeReq("projectInfo")
	second := readResp()

	pw.Close()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("RunLSP: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunLSP did not exit after pipe closed")
	}

	artifact1, _ := first["result"].(map[string]interface{})["artifact"].(string)
	artifact2, _ := second["result"].(map[string]interface{})["artifact"].(string)

	if artifact1 != "lsp-test" {
		t.Errorf("first artifact = %q, want lsp-test", artifact1)
	}
	if artifact2 != "lsp-test-modified" {
		t.Errorf("second artifact = %q, want lsp-test-modified (cache not invalidated)", artifact2)
	}
}

// TestLSPUnknownMethod checks that an unknown method returns an error, not a crash.
func TestLSPUnknownMethod(t *testing.T) {
	dir := t.TempDir()
	writePOM(t, dir)

	outBytes := runLSP(t, dir, func(in *bytes.Buffer) {
		sendRequest(t, in, "nonExistentMethod", nil)
	})

	dec := json.NewDecoder(bytes.NewReader(outBytes))
	var resp map[string]interface{}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] == nil || resp["error"] == "" {
		t.Error("expected a non-empty error for unknown method")
	}
}
