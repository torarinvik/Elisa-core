package semantic

import (
	"elisacore/src/smt"
	"sync"
	"testing"
	"time"
)

type reentrantFlightSolver struct {
	analyzer     *Analyzer
	query        string
	depth        int
	observedOpen bool
	waiter       <-chan smtQueryResult
}

func (s *reentrantFlightSolver) Check(query string) (smt.Result, time.Duration) {
	s.depth++
	defer func() { s.depth-- }()
	if s.depth == 2 {
		return smt.Unknown, 0
	}
	if s.depth != 1 {
		panic("unexpected recursive solver depth")
	}
	outer := s.analyzer.parallel.leading[query]
	s.analyzer.smtCheckRawQueryWithLogLabel(query, "nested", query)
	p := s.analyzer.parallel
	current, exists := p.smtFlights.Load(query)
	if outer != nil && p.leading[query] == outer && exists && current == outer {
		select {
		case <-outer.done:
		default:
			s.observedOpen = true
		}
	}
	other := &Analyzer{parallel: &parallelWorkerState{smtFlights: p.smtFlights}}
	result := make(chan smtQueryResult, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		value, _, _ := other.awaitSharedSMTQuery(query)
		result <- value
	}()
	<-started
	s.waiter = result
	return smt.Unsat, 0
}
func (s *reentrantFlightSolver) CheckValues(q string, _ []string) (smt.Result, map[string]string, time.Duration) {
	r, d := s.Check(q)
	return r, nil, d
}
func (*reentrantFlightSolver) Stats() smt.Stats { return smt.Stats{} }
func (*reentrantFlightSolver) Close() error     { return nil }

func TestParallelSMTReentrantMissKeepsOuterFlight(t *testing.T) {
	t.Setenv("ELISACORE_SMT_CACHE_DIR", "")
	flights := &sync.Map{}
	a := &Analyzer{smtEnabled: true, parallel: &parallelWorkerState{smtFlights: flights}}
	solver := &reentrantFlightSolver{analyzer: a, query: "(assert false)\n"}
	a.smtSolver = solver
	proved, _ := a.smtCheckRawQueryWithLogLabel(solver.query, "outer", solver.query)
	if !solver.observedOpen {
		t.Fatal("nested unknown released the outer flight")
	}
	if !proved {
		t.Fatal("outer unsat not proven")
	}
	select {
	case result := <-solver.waiter:
		if !result.proven {
			t.Fatalf("concurrent waiter did not receive proven verdict: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent waiter was not released")
	}
	other := &Analyzer{parallel: &parallelWorkerState{smtFlights: a.parallel.smtFlights}}
	shared, hit, flight := other.awaitSharedSMTQuery(solver.query)
	if !hit || !shared.proven || flight != nil {
		t.Fatalf("resolved flight was not shared: result=%+v hit=%v flight=%p", shared, hit, flight)
	}
	if _, exists := a.parallel.leading[solver.query]; exists {
		t.Fatal("outer ownership not cleared")
	}
}

func TestSMTFlightReleaseRequiresMatchingOwner(t *testing.T) {
	shared := &sync.Map{}
	a := &Analyzer{parallel: &parallelWorkerState{smtFlights: shared}}
	query := "(assert false)\n"
	_, hit, first := a.awaitSharedSMTQuery(query)
	if hit || first == nil {
		t.Fatal("missing first leader")
	}
	_, hit, nested := a.awaitSharedSMTQuery(query)
	if hit || nested != nil {
		t.Fatal("recursive call acquired outer flight")
	}
	a.releaseSMTFlight(query, nested)
	if v, ok := shared.Load(query); !ok || v != first {
		t.Fatal("nil release removed active flight")
	}
	select {
	case <-first.done:
		t.Fatal("nil release closed active flight")
	default:
	}
	a.releaseSMTFlight(query, first)
	if _, ok := shared.Load(query); ok {
		t.Fatal("owner release retained failed flight")
	}
	select {
	case <-first.done:
	default:
		t.Fatal("owner release did not unblock waiters")
	}
	_, hit, second := a.awaitSharedSMTQuery(query)
	if hit || second == nil || second == first {
		t.Fatal("new flight was not acquired")
	}
	a.releaseSMTFlight(query, first)
	if v, ok := shared.Load(query); !ok || v != second {
		t.Fatal("stale owner released newer flight")
	}
	a.resolveSMTFlight(query, smtQueryResult{proven: true})
	select {
	case <-second.done:
		if !second.ok || !second.result.proven {
			t.Fatal("resolved verdict missing")
		}
	default:
		t.Fatal("resolution did not unblock waiters")
	}
	a.releaseSMTFlight(query, second)
	if v, ok := shared.Load(query); !ok || v != second {
		t.Fatal("defer removed published verdict")
	}
}
