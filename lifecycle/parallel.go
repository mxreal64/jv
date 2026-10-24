// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package lifecycle

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/mxreal64/jv/resolver"
)

// RunReactor executes module lifecycles, respecting dependencies.
// When goal is compile or test-compile, modules with no unfinished dependencies
// are executed concurrently up to min(len(modules), runtime.NumCPU()).
// Other goals (e.g. test, package, install) execute sequentially in topological order.
func RunReactor(
	ctx context.Context,
	modules []*resolver.Module,
	goal Phase,
	buildLC func(*resolver.Module) *Lifecycle,
	logf func(string, ...interface{}),
) error {
	if len(modules) == 0 {
		return nil
	}

	// Sequential execution for post-compile phases or single module
	isParallelGoal := goal == "compile" || goal == "test-compile" || (IndexOf(goal) >= 0 && IndexOf(goal) <= IndexOf("test-compile"))
	if len(modules) == 1 || !isParallelGoal {
		for _, m := range modules {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			lc := buildLC(m)
			if lc == nil {
				continue
			}
			bc := BuildContext{
				Group:    m.Eff.G,
				Artifact: m.Eff.A,
				Version:  m.Eff.V,
				Dir:      m.Dir,
			}
			if err := lc.RunUpTo(ctx, goal, bc); err != nil {
				return fmt.Errorf("module %s: %w", m.Eff.A, err)
			}
		}
		return nil
	}

	// Map modules by GAV and GA
	byGAV := make(map[string]*resolver.Module, len(modules))
	byGA := make(map[string]*resolver.Module, len(modules))
	for _, m := range modules {
		byGAV[m.GAV()] = m
		byGA[m.Eff.G+":"+m.Eff.A] = m
	}

	// Build dependency graph (dependents: who depends on m; inDegree: how many deps m has)
	dependents := make(map[*resolver.Module][]*resolver.Module, len(modules))
	inDegree := make(map[*resolver.Module]int, len(modules))

	for _, m := range modules {
		depsSeen := make(map[*resolver.Module]bool)
		if m.Eff != nil {
			for _, d := range m.Eff.Deps {
				var depMod *resolver.Module
				if dm := byGAV[d.GroupID+":"+d.ArtifactID+":"+d.Version]; dm != nil && dm != m {
					depMod = dm
				} else if dm := byGA[d.GroupID+":"+d.ArtifactID]; dm != nil && dm != m {
					depMod = dm
				}
				if depMod != nil && !depsSeen[depMod] {
					depsSeen[depMod] = true
					dependents[depMod] = append(dependents[depMod], m)
					inDegree[m]++
				}
			}
		}
	}

	workers := len(modules)
	if n := runtime.NumCPU(); workers > n {
		workers = n
	}
	if workers < 1 {
		workers = 1
	}

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

					var err error
					lc := buildLC(m)
					if lc != nil {
						bc := BuildContext{
							Group:    m.Eff.G,
							Artifact: m.Eff.A,
							Version:  m.Eff.V,
							Dir:      m.Dir,
						}
						err = lc.RunUpTo(ctx, goal, bc)
					}

					mu.Lock()
					if err != nil {
						if firstErr == nil {
							firstErr = fmt.Errorf("module %s: %w", m.Eff.A, err)
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
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}
