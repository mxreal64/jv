// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"

	"github.com/mxreal64/jv/resolver"
)

// moduleGraph links every module to the modules that depend on it. Only dependencies on other
// modules in the slice count, so a -pl selection builds correctly.
func moduleGraph(modules []*resolver.Module) (dependents map[*resolver.Module][]*resolver.Module, inDegree map[*resolver.Module]int) {
	byGAV := make(map[string]*resolver.Module, len(modules))
	byGA := make(map[string]*resolver.Module, len(modules))
	for _, m := range modules {
		byGAV[m.GAV()] = m
		byGA[m.Eff.G+":"+m.Eff.A] = m
	}
	dependents = make(map[*resolver.Module][]*resolver.Module, len(modules))
	inDegree = make(map[*resolver.Module]int, len(modules))
	for _, m := range modules {
		if m.Eff == nil {
			continue
		}
		seen := make(map[*resolver.Module]bool)
		for _, d := range m.Eff.Deps {
			var dep *resolver.Module
			if dm := byGAV[d.GroupID+":"+d.ArtifactID+":"+d.Version]; dm != nil && dm != m {
				dep = dm
			} else if dm := byGA[d.GroupID+":"+d.ArtifactID]; dm != nil && dm != m {
				dep = dm
			}
			if dep != nil && !seen[dep] {
				seen[dep] = true
				dependents[dep] = append(dependents[dep], m)
				inDegree[m]++
			}
		}
	}
	return dependents, inDegree
}

// DefaultWorkers is the number of modules built at once when the caller has no preference:
// the JV_JOBS environment variable if it is a positive number, otherwise the CPU count.
func DefaultWorkers() int {
	if v := os.Getenv("JV_JOBS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if n := runtime.NumCPU(); n > 1 {
		return n
	}
	return 1
}

// RunGraph calls fn for every module. A module starts only after every module it depends on has
// finished successfully; at most workers modules run at once. The first error cancels the context
// passed to fn, stops scheduling and is returned as is. fn runs concurrently with itself, so it
// must not depend on process-wide state such as the working directory.
func RunGraph(ctx context.Context, modules []*resolver.Module, workers int, fn func(context.Context, *resolver.Module) error) error {
	if len(modules) == 0 {
		return nil
	}
	dependents, inDegree := moduleGraph(modules)

	// A cycle would leave modules waiting forever; refuse it up front (Kahn's algorithm).
	rem := make(map[*resolver.Module]int, len(modules))
	var queue []*resolver.Module
	for _, m := range modules {
		rem[m] = inDegree[m]
		if rem[m] == 0 {
			queue = append(queue, m)
		}
	}
	visited := 0
	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]
		visited++
		for _, d := range dependents[m] {
			rem[d]--
			if rem[d] == 0 {
				queue = append(queue, d)
			}
		}
	}
	if visited != len(modules) {
		return errors.New("dependency cycle between modules")
	}

	if workers < 1 {
		workers = 1
	}
	if workers > len(modules) {
		workers = len(modules)
	}

	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ready := make(chan *resolver.Module, len(modules))
	var mu sync.Mutex
	var firstErr error
	completed := 0
	for _, m := range modules {
		if inDegree[m] == 0 {
			ready <- m
		}
	}

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case m, ok := <-ready:
					if !ok {
						return
					}
					err := fn(ctx, m)
					mu.Lock()
					if err != nil {
						if firstErr == nil {
							firstErr = err
							cancel()
						}
						mu.Unlock()
						return
					}
					completed++
					for _, dep := range dependents[m] {
						inDegree[dep]--
						if inDegree[dep] == 0 {
							ready <- dep
						}
					}
					if completed == len(modules) {
						close(ready)
					}
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	return parent.Err()
}

// RunReactor executes the lifecycle of every module up to goal, respecting module dependencies.
// Goals up to test-compile build independent modules in parallel (DefaultWorkers); later goals
// (test, package, install, ...) run one module at a time, like Maven, so tests never overlap.
func RunReactor(
	ctx context.Context,
	modules []*resolver.Module,
	goal Phase,
	buildLC func(*resolver.Module) *Lifecycle,
	logf func(string, ...interface{}),
) error {
	return RunReactorN(ctx, modules, goal, buildLC, logf, DefaultWorkers())
}

// RunReactorN is RunReactor with an explicit number of parallel workers.
func RunReactorN(
	ctx context.Context,
	modules []*resolver.Module,
	goal Phase,
	buildLC func(*resolver.Module) *Lifecycle,
	logf func(string, ...interface{}),
	workers int,
) error {
	if IndexOf(goal) > IndexOf("test-compile") {
		workers = 1
	}
	return RunGraph(ctx, modules, workers, func(ctx context.Context, m *resolver.Module) error {
		lc := buildLC(m)
		if lc == nil {
			return nil
		}
		bc := BuildContext{Group: m.Eff.G, Artifact: m.Eff.A, Version: m.Eff.V, Dir: m.Dir}
		if err := lc.RunUpTo(ctx, goal, bc); err != nil {
			return fmt.Errorf("module %s: %w", m.Eff.A, err)
		}
		return nil
	})
}
