// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package lifecycle

import (
	"context"
	"errors"
	"strings"
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

	// explicit worker count: the test must not depend on the CPU count of the machine
	err := RunReactorN(context.Background(), modules, "compile", buildLC, nil, 3)
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

func TestRunGraph_DetectsCycle(t *testing.T) {
	a := makeMockModule("a", "b")
	b := makeMockModule("b", "a")
	err := RunGraph(context.Background(), []*resolver.Module{a, b}, 2, func(context.Context, *resolver.Module) error { return nil })
	if err == nil {
		t.Fatal("expected a cycle error")
	}
}

func TestRunGraph_RespectsWorkerLimit(t *testing.T) {
	var mods []*resolver.Module
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		mods = append(mods, makeMockModule(n))
	}
	var running, peak int32
	err := RunGraph(context.Background(), mods, 2, func(context.Context, *resolver.Module) error {
		cur := atomic.AddInt32(&running, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&running, -1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if peak != 2 {
		t.Errorf("expected exactly 2 modules at a time, peak was %d", peak)
	}
}

func TestDefaultWorkersHonoursJVJobs(t *testing.T) {
	t.Setenv("JV_JOBS", "7")
	if n := DefaultWorkers(); n != 7 {
		t.Errorf("JV_JOBS=7 gave %d workers", n)
	}
	t.Setenv("JV_JOBS", "nonsense")
	if n := DefaultWorkers(); n < 1 {
		t.Errorf("invalid JV_JOBS must fall back to the CPU count, got %d", n)
	}
}

func TestRunAfterContinuesWhereRunUpToStopped(t *testing.T) {
	var order []string
	lc := New()
	for _, ph := range []Phase{"compile", "test-compile", "test", "package"} {
		ph := ph
		lc.Add(FuncExecutor{PhaseVal: ph, IDVal: string(ph), Fn: func(context.Context, BuildContext) error {
			order = append(order, string(ph))
			return nil
		}})
	}
	if err := lc.RunUpTo(context.Background(), "test-compile", BuildContext{}); err != nil {
		t.Fatal(err)
	}
	if err := lc.RunAfter(context.Background(), "test-compile", "package", BuildContext{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "compile,test-compile,test,package" {
		t.Errorf("phases ran as %s", got)
	}
	if err := lc.RunAfter(context.Background(), "nonsense", "package", BuildContext{}); err == nil {
		t.Error("an unknown phase must be rejected")
	}
}
