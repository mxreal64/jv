// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package lifecycle

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mxreal64/jv/pom"
	"github.com/mxreal64/jv/resolver"
)

func makeMockModule(artifact string, deps ...string) *resolver.Module {
	effDeps := make([]pom.Dependency, len(deps))
	for i, d := range deps {
		effDeps[i] = pom.Dependency{
			GroupID:    "com.example",
			ArtifactID: d,
			Version:    "1.0",
			Scope:      "compile",
		}
	}
	return &resolver.Module{
		Dir: "/test/" + artifact,
		Eff: &resolver.Effective{
			G:    "com.example",
			A:    artifact,
			V:    "1.0",
			Deps: effDeps,
		},
	}
}

func TestRunReactor_ParallelIndependent(t *testing.T) {
	m1 := makeMockModule("mod1")
	m2 := makeMockModule("mod2")
	m3 := makeMockModule("mod3")
	modules := []*resolver.Module{m1, m2, m3}

	var concurrentCount int32
	var maxConcurrent int32

	buildLC := func(m *resolver.Module) *Lifecycle {
		lc := New()
		lc.Add(FuncExecutor{
			PhaseVal: "compile",
			IDVal:    "compile-" + m.Eff.A,
			Fn: func(ctx context.Context, bc BuildContext) error {
				curr := atomic.AddInt32(&concurrentCount, 1)
				defer atomic.AddInt32(&concurrentCount, -1)

				for {
					max := atomic.LoadInt32(&maxConcurrent)
					if curr <= max || atomic.CompareAndSwapInt32(&maxConcurrent, max, curr) {
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
				return nil
			},
		})
		return lc
	}

	err := RunReactor(context.Background(), modules, "compile", buildLC, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if maxConcurrent < 2 {
		t.Errorf("expected concurrent execution (max >= 2), got %d", maxConcurrent)
	}
}

func TestRunReactor_DependencyOrder(t *testing.T) {
	mCore := makeMockModule("core")
	mApp := makeMockModule("app", "core")
	modules := []*resolver.Module{mCore, mApp}

	var coreFinished bool
	var mu sync.Mutex

	buildLC := func(m *resolver.Module) *Lifecycle {
		lc := New()
		lc.Add(FuncExecutor{
			PhaseVal: "compile",
			IDVal:    "compile-" + m.Eff.A,
			Fn: func(ctx context.Context, bc BuildContext) error {
				mu.Lock()
				defer mu.Unlock()
				if m.Eff.A == "app" {
					if !coreFinished {
						return errors.New("app started before core finished")
					}
				}
				if m.Eff.A == "core" {
					time.Sleep(30 * time.Millisecond)
					coreFinished = true
				}
				return nil
			},
		})
		return lc
	}

	err := RunReactor(context.Background(), modules, "compile", buildLC, nil)
	if err != nil {
		t.Fatalf("dependency order error: %v", err)
	}
}

func TestRunReactor_FailureHaltsDependents(t *testing.T) {
	mCore := makeMockModule("core")
	mApp := makeMockModule("app", "core")
	modules := []*resolver.Module{mCore, mApp}

	var appAttempted int32

	buildLC := func(m *resolver.Module) *Lifecycle {
		lc := New()
		lc.Add(FuncExecutor{
			PhaseVal: "compile",
			IDVal:    "compile-" + m.Eff.A,
			Fn: func(ctx context.Context, bc BuildContext) error {
				if m.Eff.A == "core" {
					return errors.New("core failed compilation")
				}
				if m.Eff.A == "app" {
					atomic.StoreInt32(&appAttempted, 1)
				}
				return nil
			},
		})
		return lc
	}

	err := RunReactor(context.Background(), modules, "compile", buildLC, nil)
	if err == nil {
		t.Fatalf("expected error from failed module, got nil")
	}

	if atomic.LoadInt32(&appAttempted) != 0 {
		t.Errorf("dependent module was attempted after prerequisite failed")
	}
}

func TestRunReactor_SequentialNonCompilePhases(t *testing.T) {
	m1 := makeMockModule("mod1")
	m2 := makeMockModule("mod2")
	modules := []*resolver.Module{m1, m2}

	var running int32
	var overlapped bool

	buildLC := func(m *resolver.Module) *Lifecycle {
		lc := New()
		lc.Add(FuncExecutor{
			PhaseVal: "package",
			IDVal:    "pkg-" + m.Eff.A,
			Fn: func(ctx context.Context, bc BuildContext) error {
				if atomic.AddInt32(&running, 1) > 1 {
					overlapped = true
				}
				time.Sleep(30 * time.Millisecond)
				atomic.AddInt32(&running, -1)
				return nil
			},
		})
		return lc
	}

	err := RunReactor(context.Background(), modules, "package", buildLC, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if overlapped {
		t.Errorf("package phase should run sequentially, but overlapped execution detected")
	}
}
