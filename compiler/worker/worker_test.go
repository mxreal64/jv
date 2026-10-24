// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorker_FallbackMode(t *testing.T) {
	t.Setenv("JV_JAVAC_WORKER", "0")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	w, err := Start(ctx, "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	if w.socketPath != "" {
		t.Fatalf("expected empty socketPath in fallback mode, got %q", w.socketPath)
	}

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "Hello.java")
	if err := os.WriteFile(srcFile, []byte("public class Hello {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "classes")
	_ = os.MkdirAll(outDir, 0o755)

	if err := w.Compile(ctx, []string{"-d", outDir, srcFile}, tmpDir); err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "Hello.class")); err != nil {
		t.Fatalf("expected Hello.class to exist: %v", err)
	}
}

func TestWorker_DaemonMode(t *testing.T) {
	t.Setenv("JV_JAVAC_WORKER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	w, err := Start(ctx, "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	if w.socketPath == "" {
		t.Skip("worker daemon failed to bind socket (environment may restrict unix sockets); skipping")
	}

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "WorkerHello.java")
	if err := os.WriteFile(srcFile, []byte("public class WorkerHello { public static void main(String[] a){} }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "classes")
	_ = os.MkdirAll(outDir, 0o755)

	if err := w.Compile(ctx, []string{"-d", outDir, srcFile}, tmpDir); err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(outDir, "WorkerHello.class")); err != nil {
		t.Fatalf("expected WorkerHello.class to exist: %v", err)
	}
}

func TestWorker_ErrorPropagation(t *testing.T) {
	t.Setenv("JV_JAVAC_WORKER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	w, err := Start(ctx, "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "Bad.java")
	if err := os.WriteFile(srcFile, []byte("public class Bad { this is not java code }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "classes")
	_ = os.MkdirAll(outDir, 0o755)

	err = w.Compile(ctx, []string{"-d", outDir, srcFile}, tmpDir)
	if err == nil {
		t.Fatalf("expected compilation error, got nil")
	}
}

func TestWorker_MultipleCompiles(t *testing.T) {
	t.Setenv("JV_JAVAC_WORKER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	w, err := Start(ctx, "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer w.Stop()

	tmpDir := t.TempDir()

	for i := 1; i <= 3; i++ {
		srcFile := filepath.Join(tmpDir, "Class"+string(rune('A'+i))+".java")
		content := "public class Class" + string(rune('A'+i)) + " {}\n"
		if err := os.WriteFile(srcFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		outDir := filepath.Join(tmpDir, "classes")
		_ = os.MkdirAll(outDir, 0o755)

		if err := w.Compile(ctx, []string{"-d", outDir, srcFile}, tmpDir); err != nil {
			t.Fatalf("compile %d failed: %v", i, err)
		}

		classFile := filepath.Join(outDir, "Class"+string(rune('A'+i))+".class")
		if _, err := os.Stat(classFile); err != nil {
			t.Fatalf("expected %s to exist: %v", classFile, err)
		}
	}
}
