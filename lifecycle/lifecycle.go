// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

// Package lifecycle implements Maven's default build lifecycle as an ordered list of
// named phases, each bound to zero or more executors. RunUpTo executes every
// executor whose phase comes at or before the requested goal, in phase order.
package lifecycle

import (
	"context"
	"fmt"
)

// Phase is one step in the default lifecycle.
type Phase string

// DefaultPhases is Maven's default lifecycle in execution order.
var DefaultPhases = []Phase{
	"validate",
	"initialize",
	"generate-sources",
	"process-sources",
	"generate-resources",
	"process-resources",
	"compile",
	"process-classes",
	"generate-test-sources",
	"process-test-sources",
	"generate-test-resources",
	"process-test-resources",
	"test-compile",
	"process-test-classes",
	"test",
	"prepare-package",
	"package",
	"verify",
	"install",
	"deploy",
}

var phaseIndex = func() map[Phase]int {
	m := make(map[Phase]int, len(DefaultPhases))
	for i, p := range DefaultPhases {
		m[p] = i
	}
	return m
}()

// IndexOf returns the 0-based index of p in the default lifecycle, or -1 if unknown.
func IndexOf(p Phase) int {
	if i, ok := phaseIndex[p]; ok {
		return i
	}
	return -1
}

// Executor is one unit of work bound to a lifecycle phase.
type Executor interface {
	Phase() Phase
	ID() string
	Run(ctx context.Context, bc BuildContext) error
}

// FuncExecutor wraps a plain function as an Executor.
type FuncExecutor struct {
	PhaseVal Phase
	IDVal    string
	Fn       func(ctx context.Context, bc BuildContext) error
}

func (f FuncExecutor) Phase() Phase { return f.PhaseVal }
func (f FuncExecutor) ID() string   { return f.IDVal }
func (f FuncExecutor) Run(ctx context.Context, bc BuildContext) error {
	return f.Fn(ctx, bc)
}

// Lifecycle holds an ordered list of executors and runs them up to a given phase.
type Lifecycle struct {
	executors []Executor
}

// New creates an empty lifecycle.
func New() *Lifecycle { return &Lifecycle{} }

// Add appends an executor. Executors with an unknown phase are placed after "verify"
// (treated as index of "verify"+1 at run time).
func (l *Lifecycle) Add(e Executor) {
	l.executors = append(l.executors, e)
}

// RunUpTo runs every executor whose phase index is <= the index of goal,
// in the order they were added. Stops and returns on the first error.
// Returns an error if goal is not a known lifecycle phase.
func (l *Lifecycle) RunUpTo(ctx context.Context, goal Phase, bc BuildContext) error {
	target := IndexOf(goal)
	if target < 0 {
		return fmt.Errorf("unknown lifecycle phase %q", goal)
	}
	verifyIdx := IndexOf("verify")
	for _, e := range l.executors {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ei := IndexOf(e.Phase())
		if ei < 0 {
			ei = verifyIdx + 1 // unknown phases run after verify, before install
		}
		if ei > target {
			continue
		}
		if err := e.Run(ctx, bc); err != nil {
			return fmt.Errorf("[%s] %w", e.ID(), err)
		}
	}
	return nil
}

// Executors returns a copy of the executor list.
func (l *Lifecycle) Executors() []Executor {
	out := make([]Executor, len(l.executors))
	copy(out, l.executors)
	return out
}
