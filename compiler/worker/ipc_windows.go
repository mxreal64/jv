// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

//go:build windows

package worker

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// workerSocketPath returns empty string on Windows; the Java server picks a TCP port.
func workerSocketPath(_ string) string {
	return ""
}

// dialWorker connects to the worker daemon via TCP loopback.
func dialWorker(addr string) (net.Conn, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, fmt.Errorf("timed out connecting to worker at %s", addr)
}

// serverCmdArgs returns "--tcp" to tell the Java server to use TCP loopback mode.
func serverCmdArgs(_ string) []string {
	return []string{"--tcp"}
}

// parseReadyAddr parses "READY:127.0.0.1:PORT" from the server's ready line.
func parseReadyAddr(readyLine string, _ string) (string, error) {
	const prefix = "READY:"
	if !strings.HasPrefix(readyLine, prefix) {
		return "", fmt.Errorf("unexpected worker ready line: %q", readyLine)
	}
	addr := strings.TrimPrefix(readyLine, prefix)
	if addr == "" {
		return "", fmt.Errorf("worker ready line has empty address: %q", readyLine)
	}
	return addr, nil
}
