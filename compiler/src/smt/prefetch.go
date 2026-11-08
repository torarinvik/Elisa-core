package smt

import "slices"

// maxPrefetchHelpers bounds the helper processes one independent solver may run speculative
// checks on. One is enough to overlap a cheap pre-check with the check that follows it; a
// second absorbs a discarded speculation that is still draining.
const maxPrefetchHelpers = 2

// Prefetch is a speculative check running on a helper process. Independent mode resets
// before every check, so its verdict is the one the owning solver would compute for the
// same assertions and budget; TakePrefetch hands it over as if the owner had run it.
type Prefetch struct {
	done       chan struct{}
	helper     *procSolver
	assertions string
	vars       []string
	budget     int64 // 0 = the session budget (CheckValues), else CheckBudgetValues
	result     Result
	model      map[string]string

	statsBefore                                 Stats
	resourceBefore, transientBefore, snapBefore uint64
}

// Prefetcher is implemented by solvers that can run one check speculatively.
type Prefetcher interface {
	StartPrefetch(assertions string, vars []string, budget int64, label string) *Prefetch
	TakePrefetch(p *Prefetch, assertions string, vars []string, budget int64) (Result, map[string]string, bool)
	DiscardPrefetch(p *Prefetch)
}

// StartPrefetch begins checking `assertions` on an idle helper process and returns at once.
// It returns nil (run the check normally) when the solver is not independent, is broken or
// closed, or has no helper available.
func (s *procSolver) StartPrefetch(assertions string, vars []string, budget int64, label string) *Prefetch {
	if budget < 0 {
		return nil
	}
	s.mu.Lock()
	if !s.independent || s.broken || s.closed || !s.hasOpts {
		s.mu.Unlock()
		return nil
	}
	var helper *procSolver
	if n := len(s.idleHelpers); n > 0 {
		helper = s.idleHelpers[n-1]
		s.idleHelpers = s.idleHelpers[:n-1]
	} else if s.helperCount < maxPrefetchHelpers {
		s.helperCount++
		s.mu.Unlock()
		opened, err := Open(s.opts)
		if err != nil || opened == nil {
			if opened != nil {
				_ = opened.Close()
			}
			s.mu.Lock()
			s.helperCount--
			s.mu.Unlock()
			return nil
		}
		helper = opened.(*procSolver)
		s.mu.Lock()
		s.stats.SpawnMillis += helper.stats.SpawnMillis
	}
	if helper == nil {
		s.mu.Unlock()
		return nil
	}
	s.prefetchInFlight.Add(1)
	s.mu.Unlock()

	helper.SetLogLabel(label)
	p := &Prefetch{done: make(chan struct{}), helper: helper, assertions: assertions, vars: slices.Clone(vars), budget: budget}
	p.statsBefore = helper.Stats()
	p.resourceBefore, p.transientBefore = helper.UnknownCacheEpoch()
	p.snapBefore = helper.SnapshotUnknownEpoch()
	go func() {
		defer close(p.done)
		if budget > 0 {
			p.result, p.model = helper.CheckBudgetValues(assertions, p.vars, budget)
		} else {
			p.result, p.model, _ = helper.CheckValues(assertions, p.vars)
		}
	}()
	return p
}

// TakePrefetch returns p's verdict when it checked exactly these assertions, vars and budget,
// folding the helper's statistics and Unknown classification into this solver so callers
// comparing epochs see the same accounting a direct check would produce. Otherwise it
// discards p and reports false.
func (s *procSolver) TakePrefetch(p *Prefetch, assertions string, vars []string, budget int64) (Result, map[string]string, bool) {
	if p == nil {
		return Unknown, nil, false
	}
	if p.assertions != assertions || p.budget != budget || !slices.Equal(p.vars, vars) {
		s.DiscardPrefetch(p)
		return Unknown, nil, false
	}
	<-p.done
	resource, transient := p.helper.UnknownCacheEpoch()
	snap := p.helper.SnapshotUnknownEpoch()
	s.mu.Lock()
	s.mergeHelperStatsLocked(p)
	s.resourceUnknown += resource - p.resourceBefore
	s.transientUnknown += transient - p.transientBefore
	s.snapshotUnknown += snap - p.snapBefore
	s.mu.Unlock()
	s.releaseHelper(p.helper)
	return p.result, p.model, true
}

// DiscardPrefetch abandons p. The helper finishes in the background; only its timing
// statistics are folded in, never its Unknown classification, so a discarded speculation
// cannot change how any other check is cached.
func (s *procSolver) DiscardPrefetch(p *Prefetch) {
	if p == nil {
		return
	}
	go func() {
		<-p.done
		s.mu.Lock()
		s.mergeHelperStatsLocked(p)
		s.mu.Unlock()
		s.releaseHelper(p.helper)
	}()
}

func (s *procSolver) mergeHelperStatsLocked(p *Prefetch) {
	after := p.helper.Stats()
	s.stats.Queries += after.Queries - p.statsBefore.Queries
	s.stats.Sat += after.Sat - p.statsBefore.Sat
	s.stats.Unsat += after.Unsat - p.statsBefore.Unsat
	s.stats.Unknown += after.Unknown - p.statsBefore.Unknown
	s.stats.Total += after.Total - p.statsBefore.Total
	s.stats.Slowest = max(s.stats.Slowest, after.Slowest)
}

func (s *procSolver) releaseHelper(helper *procSolver) {
	helper.mu.Lock()
	broken := helper.broken
	helper.mu.Unlock()
	s.mu.Lock()
	if s.closed || broken {
		s.helperCount--
		s.mu.Unlock()
		_ = helper.Close()
	} else {
		s.idleHelpers = append(s.idleHelpers, helper)
		s.mu.Unlock()
	}
	s.prefetchInFlight.Done()
}

// closeHelpers waits for in-flight speculation and closes every helper process.
func (s *procSolver) closeHelpers() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.prefetchInFlight.Wait()
	s.mu.Lock()
	idle := s.idleHelpers
	s.idleHelpers = nil
	s.helperCount -= len(idle)
	s.mu.Unlock()
	for _, helper := range idle {
		_ = helper.Close()
	}
}
