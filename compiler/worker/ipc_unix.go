// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

//go:build !windows

package worker

import (
	"fmt"
	"net"
	"os"
)

// workerSocketPath returns the Unix domain socket path for the javac worker daemon.
func workerSocketPath(tmpDir string) string {
	return fmt.Sprintf("%s/javac-%d.sock", tmpDir, os.Getpid())
}

// dialWorker connects to the worker daemon over a Unix domain socket.
func dialWorker(addr string) (net.Conn, error) {
	return net.Dial("unix", addr)
}

// serverCmdArgs returns the command-line arguments to append to the Java server invocation.
func serverCmdArgs(addr string) []string {
	return []string{addr}
}

// parseReadyAddr extracts the connectable address from the server's READY line.
// On Unix the server prints "READY" and the addr is returned unchanged.
func parseReadyAddr(readyLine string, addr string) (string, error) {
	if readyLine != "READY" {
		return "", fmt.Errorf("unexpected worker ready line: %q", readyLine)
	}
	return addr, nil
}
