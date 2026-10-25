package semantic

import (
	"elisacore/src/smt"
	"testing"
	"time"
)

type retryUnknownSolver struct {
	fixedSMTSolver
	calls               int
	resource, transient uint64
	certified           bool
}

func (s *retryUnknownSolver) UnknownCacheEpoch() (uint64, uint64) { return s.resource, s.transient }
func (s *retryUnknownSolver) CheckValues(string, []string) (smt.Result, map[string]string, time.Duration) {
	s.calls++
	if s.calls == 1 {
		if s.certified {
			s.resource++
		} else {
			s.transient++
		}
		return smt.Unknown, nil, 0
	}
	return smt.Unsat, nil, 0
}

func TestUnknownTransientRetriesButCertifiedResourceDeclineCaches(t *testing.T) {
	priorIndependent, priorDecline := smtIndependent, smtDeclineCacheEnabled
	smtIndependent, smtDeclineCacheEnabled = true, true
	defer func() { smtIndependent, smtDeclineCacheEnabled = priorIndependent, priorDecline }()
	for _, certified := range []bool{false, true} {
		solver := &retryUnknownSolver{certified: certified}
		cache := &smtProofCache{directory: t.TempDir(), identity: "unknown-certificate"}
		a := &Analyzer{smtEnabled: true, smtSolver: solver, smtProofCacheInitialized: true, smtProofCache: cache}
		tr, query := smtQueryForCacheTest(a, "", "true")
		if proven, _ := a.smtCheckQuery(tr, "", "true"); proven {
			t.Fatal("unknown became proof")
		}
		if _, ok := a.smtQueryCache[query]; ok != certified {
			t.Fatalf("memory certificate mismatch: %v", ok)
		}
		if _, ok := cache.declined(query); ok != certified {
			t.Fatalf("disk certificate mismatch: %v", ok)
		}
		tr, _ = smtQueryForCacheTest(a, "", "true")
		proven, _ := a.smtCheckQuery(tr, "", "true")
		if certified {
			smtIndependent = false
			if _, ok := cache.declined(query); ok {
				t.Fatal("session mode reused independent resource decline")
			}
			smtIndependent = true
			if proven || solver.calls != 1 {
				t.Fatal("certified decline did not reuse")
			}
		} else if !proven || solver.calls != 2 {
			t.Fatal("transient failure hid successful retry")
		}
	}
}

type compoundUnknownSolver struct {
	fixedSMTSolver
	resource, transient uint64
	calls               int
}

func (s *compoundUnknownSolver) UnknownCacheEpoch() (uint64, uint64) { return s.resource, s.transient }
func (s *compoundUnknownSolver) CheckValues(string, []string) (smt.Result, map[string]string, time.Duration) {
	s.calls++
	if s.calls == 1 {
		s.transient++
	} else {
		s.resource++
	}
	return smt.Unknown, nil, 0
}
func (s *compoundUnknownSolver) CheckBudgetValues(q string, v []string, _ int64) (smt.Result, map[string]string) {
	r, m, _ := s.CheckValues(q, v)
	return r, m
}
func (s *compoundUnknownSolver) CheckBudget(q string, b int64) smt.Result {
	r, _ := s.CheckBudgetValues(q, nil, b)
	return r
}

func TestUnknownLaterResourceFailureCannotConcealTransientWeakening(t *testing.T) {
	prior := smtIndependent
	smtIndependent = true
	defer func() { smtIndependent = prior }()
	solver := &compoundUnknownSolver{}
	a := &Analyzer{smtEnabled: true, smtSolver: solver, smtProofCacheInitialized: true}
	hyps := "(assert (forall ((i Int)) (= i i)))\n"
	tr, query := smtQueryForCacheTest(a, hyps, "true")
	a.smtCheckQuery(tr, hyps, "true")
	if solver.resource == 0 || solver.transient != 1 {
		t.Fatalf("compound fixture did not exercise both results: %+v", solver)
	}
	if _, ok := a.smtQueryCache[query]; ok {
		t.Fatal("later resource unknown concealed earlier transient")
	}
	tr, _ = smtQueryForCacheTest(a, hyps, "true")
	a.smtCheckQuery(tr, hyps, "true")
	if _, ok := a.smtQueryCache[query]; !ok {
		t.Fatal("fully resource-certified retry was not cached")
	}
}
