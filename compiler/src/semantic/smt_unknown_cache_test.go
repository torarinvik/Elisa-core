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

type snapshotUnknownSolver struct {
	fixedSMTSolver
	snapshot uint64
	transient uint64
	alsoTransient bool
}

func (s *snapshotUnknownSolver) UnknownCacheEpoch() (uint64, uint64) { return 0, s.transient }
func (s *snapshotUnknownSolver) SnapshotUnknownEpoch() uint64 { return s.snapshot }
func (s *snapshotUnknownSolver) CheckValues(string, []string) (smt.Result, map[string]string, time.Duration) {
	s.snapshot++
	if s.alsoTransient { s.transient++ }
	return smt.Unknown, nil, 0
}

func TestHistoryCertificateCannotHideTransientFailure(t *testing.T) {
	prior := smtIndependent
	smtIndependent = true
	defer func() { smtIndependent = prior }()
	solver := &snapshotUnknownSolver{alsoTransient: true}
	a := &Analyzer{smtEnabled: true, smtSolver: solver, smtProofCacheInitialized: true}
	tr, _ := smtQueryForCacheTest(a, "", "true")
	a.smtCheckQuery(tr, "", "true")
	if !a.smtSnapshotTainted {
		t.Fatal("history certificate concealed transient failure")
	}
}

func TestHistoryCertifiedUnknownDoesNotEnterQueryCache(t *testing.T) {
	priorIndependent, priorDecline := smtIndependent, smtDeclineCacheEnabled
	smtIndependent, smtDeclineCacheEnabled = true, true
	defer func() { smtIndependent, smtDeclineCacheEnabled = priorIndependent, priorDecline }()
	solver := &snapshotUnknownSolver{}
	cache := &smtProofCache{directory: t.TempDir(), identity: "snapshot-only"}
	a := &Analyzer{smtEnabled: true, smtSolver: solver, smtProofCacheInitialized: true, smtProofCache: cache}
	tr, query := smtQueryForCacheTest(a, "", "true")
	if proven, _ := a.smtCheckQuery(tr, "", "true"); proven {
		t.Fatal("unknown became a proof")
	}
	if a.smtSnapshotTainted {
		t.Fatal("certified history unnecessarily tainted snapshot")
	}
	if _, ok := a.smtQueryCache[query]; ok {
		t.Fatal("history-dependent decline entered query memory cache")
	}
	if _, ok := cache.declined(query); ok {
		t.Fatal("history-dependent decline entered persistent query cache")
	}
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
		if a.smtSnapshotTainted == certified {
			t.Fatalf("snapshot taint=%v, want %v for certified=%v", a.smtSnapshotTainted, !certified, certified)
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
		if !certified && !a.smtSnapshotTainted {
			t.Fatal("successful retry cleared sticky snapshot taint")
		}
	}
}

func TestInvocationCertificateLatchesTransientFailure(t *testing.T) {
	transientSMTObserved.Store(false)
	t.Cleanup(func() { transientSMTObserved.Store(false) })
	if !CacheInvocationCertifiable() {
		t.Fatal("fresh invocation should be certifiable")
	}
	a := &Analyzer{smtEnabled: true, smtSolver: &fixedSMTSolver{result: smt.Unknown}}
	a.smtCheckRawQueryWithLogLabel("(assert true)\n", "raw", "true")
	if CacheInvocationCertifiable() {
		t.Fatal("transient unknown allowed invocation certificate")
	}
	a.resetSMTForRetainedUpdate()
	if CacheInvocationCertifiable() {
		t.Fatal("retry cleared invocation certificate taint")
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
	if !a.smtSnapshotTainted {
		t.Fatal("combined transient/resource Unknown did not taint snapshots")
	}
	tr, _ = smtQueryForCacheTest(a, hyps, "true")
	a.smtCheckQuery(tr, hyps, "true")
	if _, ok := a.smtQueryCache[query]; !ok {
		t.Fatal("fully resource-certified retry was not cached")
	}
	if !a.smtSnapshotTainted {
		t.Fatal("later certified result cleared earlier transient snapshot taint")
	}
}

func TestRawSMTUnknownTaintsSnapshot(t *testing.T) {
	a := &Analyzer{
		smtEnabled: true,
		smtSolver:  &fixedSMTSolver{result: smt.Unknown},
	}
	if proven, _ := a.smtCheckRawQueryWithLogLabel("(assert true)\n", "raw", "true"); proven {
		t.Fatal("Unknown raw query became proven")
	}
	if !a.smtSnapshotTainted {
		t.Fatal("uncached raw Unknown did not taint snapshots")
	}
}
