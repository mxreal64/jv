// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package worker

import (
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
	"time"
)

//go:embed server.jar
var serverJar []byte

//go:embed server/Server.java
var serverJavaSource []byte

// Worker manages a persistent javac worker process communicating over a Unix domain socket.
type Worker struct {
	JavaHome   string
	socketPath string
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	mu         sync.Mutex
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

// Start launches a persistent javac worker if JV_JAVAC_WORKER=1,
// otherwise returns a worker configured to fall back to direct javac execution.
func Start(ctx context.Context, javaHome string) (*Worker, error) {
	w := &Worker{JavaHome: javaHome}
	if os.Getenv("JV_JAVAC_WORKER") != "1" {
		return w, nil
	}

	if err := w.startDaemon(ctx); err != nil {
		// graceful fallback to direct javac if daemon fails to start
		return w, nil
	}
	return w, nil
}

func (w *Worker) javaExecutable() string {
	if w.JavaHome != "" {
		p := filepath.Join(w.JavaHome, "bin", "java")
		if runtime.GOOS == "windows" {
			p += ".exe"
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
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
	if w.JavaHome != "" {
		p := filepath.Join(w.JavaHome, "bin", "javac")
		if runtime.GOOS == "windows" {
			p += ".exe"
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
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
		// Fallback: compile Server.java on the fly
		srcPath := filepath.Join(tmpDir, "Server.java")
		if err := os.WriteFile(srcPath, serverJavaSource, 0o644); err != nil {
			return err
		}
		javac := w.javacExecutable()
		buildCmd := exec.CommandContext(ctx, javac, "-d", tmpDir, srcPath)
		if err := buildCmd.Run(); err != nil {
			return err
		}
		jarPath = tmpDir
	}

	sock := filepath.Join(tmpDir, fmt.Sprintf("javac-%d.sock", os.Getpid()))
	_ = os.Remove(sock)

	cmd := exec.CommandContext(ctx, w.javaExecutable(), "-cp", jarPath, "worker.server.Server", sock)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return err
	}

	w.cmd = cmd
	w.stdin = stdin
	w.socketPath = sock

	// Wait for socket to become connectable (timeout 10s)
	deadline := time.Now().Add(10 * time.Second)
	connected := false
	for time.Now().Before(deadline) {
		conn, dialErr := net.Dial("unix", sock)
		if dialErr == nil {
			conn.Close()
			connected = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !connected {
		w.Stop()
		return fmt.Errorf("timed out waiting for javac worker socket")
	}

	return nil
}

// Compile compiles Java sources using the worker daemon or falls back to direct javac.
func (w *Worker) Compile(ctx context.Context, args []string, dir string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.socketPath == "" {
		return w.compileDirect(ctx, args, dir)
	}

	// Try compiling via Unix domain socket
	conn, err := net.Dial("unix", w.socketPath)
	if err != nil {
		// Daemon connection failed; fallback to direct
		return w.compileDirect(ctx, args, dir)
	}
	defer conn.Close()

	req := compileRequest{
		Args: args,
		Dir:  dir,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return w.compileDirect(ctx, args, dir)
	}

	if uconn, ok := conn.(*net.UnixConn); ok {
		_ = uconn.CloseWrite()
	}

	var resp compileResponse
	decoder := json.NewDecoder(conn)
	if err := decoder.Decode(&resp); err != nil {
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
	bin := w.javacExecutable()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Stop shuts down the worker process and cleans up resources.
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
