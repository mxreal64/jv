// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

//go:build windows

package worker

import (
	"context"
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
// Bounded by ctx and a maximum 500ms timeout so failures fail over to direct javac immediately.
func dialWorker(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	timeout := 500 * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		if rem := time.Until(deadline); rem < timeout {
			timeout = rem
		}
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return d.DialContext(dialCtx, "tcp", addr)
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
