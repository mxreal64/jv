// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package lifecycle

import (
	"context"
	"errors"
	"testing"
)

func mkExec(phase Phase, id string, ran *[]string, fail bool) Executor {
	return FuncExecutor{
		PhaseVal: phase,
		IDVal:    id,
		Fn: func(_ context.Context, _ BuildContext) error {
			*ran = append(*ran, id)
			if fail {
				return errors.New("boom")
			}
			return nil
		},
	}
}

func TestRunUpTo_selectsCorrectPhases(t *testing.T) {
	var ran []string
	lc := New()
	lc.Add(mkExec("process-resources", "resources", &ran, false))
	lc.Add(mkExec("compile", "compile", &ran, false))
	lc.Add(mkExec("test", "test", &ran, false))
	lc.Add(mkExec("package", "jar", &ran, false))
	if err := lc.RunUpTo(context.Background(), "compile", BuildContext{}); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 2 || ran[0] != "resources" || ran[1] != "compile" {
		t.Fatalf("expected [resources compile], got %v", ran)
	}
}

func TestRunUpTo_stopsOnError(t *testing.T) {
	var ran []string
	lc := New()
	lc.Add(mkExec("compile", "compile", &ran, true))
	lc.Add(mkExec("test", "test", &ran, false))
	err := lc.RunUpTo(context.Background(), "test", BuildContext{})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(ran) != 1 || ran[0] != "compile" {
		t.Fatalf("should have stopped after compile, ran %v", ran)
	}
}

func TestRunUpTo_unknownGoalErrors(t *testing.T) {
	lc := New()
	if err := lc.RunUpTo(context.Background(), "not-a-phase", BuildContext{}); err == nil {
		t.Fatal("expected error for unknown goal")
	}
}

func TestRunUpTo_unknownExecutorPhaseRunsLate(t *testing.T) {
	var ran []string
	lc := New()
	lc.Add(mkExec("mystery-phase", "mystery", &ran, false))
	lc.Add(mkExec("compile", "compile", &ran, false))
	// mystery-phase has index -1, mapped to verify+1 > compile → must NOT run
	if err := lc.RunUpTo(context.Background(), "compile", BuildContext{}); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0] != "compile" {
		t.Fatalf("unknown-phase executor should be skipped for 'compile', ran %v", ran)
	}
}

func TestRunUpTo_unknownExecutorPhaseRunsForInstall(t *testing.T) {
	var ran []string
	lc := New()
	lc.Add(mkExec("mystery-phase", "mystery", &ran, false))
	lc.Add(mkExec("compile", "compile", &ran, false))
	// Both should run when goal is "install" (index 18, > verify+1=18... verify is 17, so +1=18=install)
	// Actually: verify is index 17, unknown maps to 18 (=install). install goal is also 18.
	// So mystery runs for "install" but not "compile".
	if err := lc.RunUpTo(context.Background(), "install", BuildContext{}); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 2 {
		t.Fatalf("both executors should run for 'install', ran %v", ran)
	}
}

func TestRunUpTo_canceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled
	lc := New()
	var ran []string
	lc.Add(mkExec("compile", "compile", &ran, false))
	err := lc.RunUpTo(ctx, "compile", BuildContext{})
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
	if len(ran) != 0 {
		t.Fatalf("no executor should run with canceled context, ran %v", ran)
	}
}

func TestRunUpTo_errorWrapsID(t *testing.T) {
	lc := New()
	var ran []string
	lc.Add(mkExec("compile", "my-executor", &ran, true))
	err := lc.RunUpTo(context.Background(), "compile", BuildContext{})
	if err == nil || !errors.Is(err, errors.New("boom")) {
		// We can't use errors.Is for a plain errors.New value but we can check the message.
		if err == nil {
			t.Fatal("expected error")
		}
		// Check the ID is in the message.
		if msg := err.Error(); len(msg) == 0 || msg[:len("[my-executor]")] != "[my-executor]" {
			t.Fatalf("error should be prefixed with [my-executor], got: %v", err)
		}
	}
}
