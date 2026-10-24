// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package worker

import (
	"context"
	"os/exec"
)

type Worker struct {
	JavaHome string
}

func Start(ctx context.Context, javaHome string) (*Worker, error) {
	return &Worker{JavaHome: javaHome}, nil
}

func (w *Worker) Compile(ctx context.Context, args []string, dir string) error {
	bin := "javac"
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	return cmd.Run()
}

func (w *Worker) Stop() {}
