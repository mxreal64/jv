// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package worker

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

//go:embed server.jar
var serverJar []byte

//go:embed server/Server.java
var serverJavaSource []byte

// Worker manages a persistent javac worker process communicating over IPC
// (Unix domain socket on Linux/macOS, TCP loopback on Windows).
type Worker struct {
	JavaHome   string
	socketPath string // IPC address: unix socket path (Unix) or "host:port" (Windows)
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	mu         sync.Mutex // guards Stop only
}

// IsActive reports whether the worker daemon is running and connectable.
func (w *Worker) IsActive() bool {
	return w.socketPath != ""
}

type compileRequest struct {
	Args []string `json:"args"`
	Dir  string   `json:"dir"`
}

type compileResponse struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// Start launches a persistent javac worker daemon. If the daemon fails to start,
// a worker configured for direct javac fallback is returned with no error.
// Set JV_JAVAC_WORKER=0 to disable the daemon entirely and always use direct javac.
func Start(ctx context.Context, javaHome string) (*Worker, error) {
	w := &Worker{JavaHome: javaHome}
	if os.Getenv("JV_JAVAC_WORKER") == "0" {
		return w, nil
	}
	// Best-effort: ignore daemon start failure and fall back to direct javac.
	_ = w.startDaemon(ctx)
	return w, nil
}

func (w *Worker) javaExecutable() string {
	for _, jh := range []string{w.JavaHome, os.Getenv("JAVA_HOME")} {
		if jh == "" {
			continue
		}
		p := filepath.Join(jh, "bin", "java")
		if runtime.GOOS == "windows" {
			p += ".exe"
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return "java"
}

func (w *Worker) javacExecutable() string {
	for _, jh := range []string{w.JavaHome, os.Getenv("JAVA_HOME")} {
		if jh == "" {
			continue
		}
		p := filepath.Join(jh, "bin", "javac")
		if runtime.GOOS == "windows" {
			p += ".exe"
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return "javac"
}

func (w *Worker) startDaemon(ctx context.Context) error {
	tmpDir, err := os.MkdirTemp("", "jv-worker-*")
	if err != nil {
		return err
	}

	jarPath := filepath.Join(tmpDir, "server.jar")
	if len(serverJar) > 0 {
		if err := os.WriteFile(jarPath, serverJar, 0o644); err != nil {
			return err
		}
	} else {
		// Fallback: compile Server.java on the fly (development mode).
		srcPath := filepath.Join(tmpDir, "Server.java")
		if err := os.WriteFile(srcPath, serverJavaSource, 0o644); err != nil {
			return err
		}
		buildCmd := exec.CommandContext(ctx, w.javacExecutable(), "--release", "17", "-d", tmpDir, srcPath)
		if err := buildCmd.Run(); err != nil {
			return err
		}
		jarPath = tmpDir
	}

	ipcAddr := workerSocketPath(tmpDir)
	javaArgs := []string{"-cp", jarPath, "worker.server.Server"}
	javaArgs = append(javaArgs, serverCmdArgs(ipcAddr)...)

	cmd := exec.CommandContext(ctx, w.javaExecutable(), javaArgs...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return err
	}

	w.cmd = cmd
	w.stdin = stdin

	// Read the READY line from the server. On Unix: "READY". On Windows: "READY:host:port".
	readyCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		if scanner.Scan() {
			readyCh <- scanner.Text()
		} else {
			close(readyCh)
		}
		// Discard any further server stdout.
		_, _ = io.Copy(io.Discard, stdoutPipe)
	}()

	select {
	case line, ok := <-readyCh:
		if !ok {
			w.Stop()
			return fmt.Errorf("worker closed stdout without sending READY")
		}
		addr, err := parseReadyAddr(line, ipcAddr)
		if err != nil {
			w.Stop()
			return err
		}
		w.socketPath = addr
	case <-ctx.Done():
		w.Stop()
		return ctx.Err()
	}

	return nil
}

// Compile compiles Java sources using the worker daemon or falls back to direct javac.
// Multiple goroutines may call Compile concurrently; each opens its own connection.
func (w *Worker) Compile(ctx context.Context, args []string, dir string) error {
	if w.socketPath == "" {
		return w.compileDirect(ctx, args, dir)
	}

	conn, err := dialWorker(w.socketPath)
	if err != nil {
		// Daemon connection failed; fall back to direct javac.
		return w.compileDirect(ctx, args, dir)
	}
	defer conn.Close()

	req := compileRequest{Args: args, Dir: dir}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return w.compileDirect(ctx, args, dir)
	}

	// Signal end-of-write for Unix connections so the server knows the request is complete.
	if uconn, ok := conn.(*net.UnixConn); ok {
		_ = uconn.CloseWrite()
	}

	var resp compileResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return w.compileDirect(ctx, args, dir)
	}

	if resp.Stdout != "" {
		fmt.Fprint(os.Stdout, resp.Stdout)
	}
	if resp.Stderr != "" {
		fmt.Fprint(os.Stderr, resp.Stderr)
	}

	if resp.ExitCode != 0 {
		return fmt.Errorf("javac exited with code %d", resp.ExitCode)
	}
	return nil
}

func (w *Worker) compileDirect(ctx context.Context, args []string, dir string) error {
	cmd := exec.CommandContext(ctx, w.javacExecutable(), args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Stop shuts down the worker process and cleans up resources.
// It is safe to call Stop once at process exit; it is not concurrency-safe with Compile.
func (w *Worker) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stdin != nil {
		_ = w.stdin.Close()
		w.stdin = nil
	}
	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
		_ = w.cmd.Wait()
		w.cmd = nil
	}
	if w.socketPath != "" {
		_ = os.Remove(w.socketPath)
		_ = os.RemoveAll(filepath.Dir(w.socketPath))
		w.socketPath = ""
	}
}
