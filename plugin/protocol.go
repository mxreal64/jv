// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/mxreal64/jv/lifecycle"
)

// Request is written to the plugin's stdin as a single JSON line.
type Request struct {
	Phase   string                 `json:"phase"`
	ID      string                 `json:"id"`
	Context BuildContextJSON       `json:"context"`
	Config  map[string]interface{} `json:"config"`
}

// Response is read from the plugin's stdout as a single JSON line.
type Response struct {
	Outputs     []string          `json:"outputs"`     // files written
	SourceRoots []string          `json:"sourceRoots"` // dirs to add to compile path
	Properties  map[string]string `json:"properties"`  // properties to set for later phases
	Skip        bool              `json:"skip"`        // nothing to do, not an error
	Message     string            `json:"message"`     // human-readable status
}

// BuildContextJSON is the build context serialized for the subprocess.
type BuildContextJSON struct {
	Group            string            `json:"group"`
	Artifact         string            `json:"artifact"`
	Version          string            `json:"version"`
	Packaging        string            `json:"packaging"`
	Dir              string            `json:"dir"`
	SourceDirs       []string          `json:"sourceDirs"`
	TestSourceDirs   []string          `json:"testSourceDirs"`
	ResourceDirs     []string          `json:"resourceDirs"`
	TestResourceDirs []string          `json:"testResourceDirs"`
	ClassesDir       string            `json:"classesDir"`
	TestClassesDir   string            `json:"testClassesDir"`
	GeneratedDir     string            `json:"generatedDir"`
	TargetDir        string            `json:"targetDir"`
	CompileCP        []string          `json:"compileCP"`
	TestCP           []string          `json:"testCP"`
	RuntimeCP        []string          `json:"runtimeCP"`
	Props            map[string]string `json:"props"`
	Repo             string            `json:"repo"`
}

// ToJSONContext converts a lifecycle.BuildContext to BuildContextJSON.
func ToJSONContext(bc lifecycle.BuildContext) BuildContextJSON {
	return BuildContextJSON{
		Group:            bc.Group,
		Artifact:         bc.Artifact,
		Version:          bc.Version,
		Packaging:        bc.Packaging,
		Dir:              bc.Dir,
		SourceDirs:       bc.AllSourceDirs(),
		TestSourceDirs:   bc.AllTestSourceDirs(),
		ResourceDirs:     bc.ResourceDirs,
		TestResourceDirs: bc.TestResourceDirs,
		ClassesDir:       bc.ClassesDir,
		TestClassesDir:   bc.TestClassesDir,
		GeneratedDir:     bc.GeneratedDir,
		TargetDir:        bc.TargetDir,
		CompileCP:        bc.CompileCP,
		TestCP:           bc.TestCP,
		RuntimeCP:        bc.RuntimeCP,
		Props:            bc.Props,
		Repo:             bc.Repo,
	}
}

// RunPlugin runs a plugin subprocess with Request written to stdin and Response read from stdout.
func RunPlugin(ctx context.Context, binary string, req Request) (Response, error) {
	ctxTimeout, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctxTimeout, binary)
	var stdin bytes.Buffer
	if err := json.NewEncoder(&stdin).Encode(req); err != nil {
		return Response{}, fmt.Errorf("encoding request: %w", err)
	}
	cmd.Stdin = &stdin

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			// Exit code 2 = Skip
			return Response{Skip: true}, nil
		}
		errMsg := stringsTrim(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return Response{}, fmt.Errorf("plugin %s failed (exit %d): %s", req.ID, exitCode(err), errMsg)
	}

	scanner := bufio.NewScanner(&stdout)
	if !scanner.Scan() {
		return Response{}, io.EOF
	}

	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("decoding plugin response: %w", err)
	}
	return resp, nil
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func stringsTrim(s string) string {
	return string(bytes.TrimSpace([]byte(s)))
}
