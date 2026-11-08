package smt

import (
	"testing"
	"time"
)

func openPrefetchTestSolver(t *testing.T) *procSolver {
	t.Helper()
	z3Available(t)
	solver, err := Open(Options{Independent: true, ResourceLimit: 1000, PerQueryTimeoutMillis: 120000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = solver.Close() })
	return solver.(*procSolver)
}

// A taken prefetch is indistinguishable from a direct check: same verdict, and its
// resource certification lands in the owner's Unknown epoch.
func TestPrefetchTakeMatchesDirectCheck(t *testing.T) {
	direct := openPrefetchTestSolver(t)
	directResult, _ := direct.Check(rlimitHardQuery)
	directResource, directTransient := direct.UnknownCacheEpoch()

	owner := openPrefetchTestSolver(t)
	p := owner.StartPrefetch(rlimitHardQuery, nil, 0, "")
	if p == nil {
		t.Fatal("no helper started")
	}
	result, _, ok := owner.TakePrefetch(p, rlimitHardQuery, nil, 0)
	if !ok || result != directResult {
		t.Fatalf("prefetch = %v (taken %v), direct = %v", result, ok, directResult)
	}
	resource, transient := owner.UnknownCacheEpoch()
	if resource != directResource || transient != directTransient {
		t.Fatalf("epoch (%d,%d), direct (%d,%d)", resource, transient, directResource, directTransient)
	}
	if owner.Stats().Queries == 0 {
		t.Fatal("helper statistics not folded in")
	}

	sat := owner.StartPrefetch("(declare-const x Int)(assert (= x 7))", []string{"x"}, 0, "")
	result, model, ok := owner.TakePrefetch(sat, "(declare-const x Int)(assert (= x 7))", []string{"x"}, 0)
	if !ok || result != Sat || model["x"] != "7" {
		t.Fatalf("sat prefetch = %v %v %v", result, model, ok)
	}
}

// A prefetch is only handed over for the exact assertions, vars and budget it checked.
func TestPrefetchTakeRejectsMismatchedCheck(t *testing.T) {
	owner := openPrefetchTestSolver(t)
	for _, tc := range []struct {
		name, assertions string
		budget           int64
	}{
		{"assertions", "(assert true)", 0},
		{"budget", "(assert false)", 500},
	} {
		p := owner.StartPrefetch("(assert false)", nil, 0, "")
		if p == nil {
			t.Fatal("no helper started")
		}
		if _, _, ok := owner.TakePrefetch(p, tc.assertions, nil, tc.budget); ok {
			t.Fatalf("%s mismatch was taken", tc.name)
		}
	}
}

// A discarded speculation folds in timing only: its Unknown classification must never
// change how another check is cached.
func TestPrefetchDiscardLeavesUnknownEpochUntouched(t *testing.T) {
	owner := openPrefetchTestSolver(t)
	p := owner.StartPrefetch(rlimitHardQuery, nil, 0, "")
	if p == nil {
		t.Fatal("no helper started")
	}
	owner.DiscardPrefetch(p)
	deadline := time.Now().Add(30 * time.Second)
	for owner.Stats().Queries == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if owner.Stats().Queries == 0 {
		t.Fatal("discarded helper never drained")
	}
	if resource, transient := owner.UnknownCacheEpoch(); resource != 0 || transient != 0 {
		t.Fatalf("discard changed epoch: %d %d", resource, transient)
	}
}

// A hand-built solver (no Options) and a closed solver never speculate.
func TestPrefetchDeclinesWithoutHelpers(t *testing.T) {
	if p := (&procSolver{independent: true}).StartPrefetch("(assert true)", nil, 0, ""); p != nil {
		t.Fatal("hand-built solver started a helper")
	}
	owner := openPrefetchTestSolver(t)
	_ = owner.Close()
	if p := owner.StartPrefetch("(assert true)", nil, 0, ""); p != nil {
		t.Fatal("closed solver started a helper")
	}
}
