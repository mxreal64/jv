// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package bridge

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// FallbackToMaven runs real mvn and returns its exit code.
func FallbackToMaven(args []string) int {
	cmd := exec.Command("mvn", args...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "[jv] could not run mvn: %v\n", err)
		return 127
	}
	return 0
}
