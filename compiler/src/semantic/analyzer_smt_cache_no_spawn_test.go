package semantic

import (
	"path/filepath"
	"testing"
	"time"

	"elisacore/src/smt"
)

type fixedSMTSolver struct {
	result smt.Result
	model  map[string]string
}

func (s *fixedSMTSolver) Check(string) (smt.Result, time.Duration) {
	return s.result, 0
}

func (s *fixedSMTSolver) CheckValues(_ string, vars []string) (smt.Result, map[string]string, time.Duration) {
	model := map[string]string{}
	for _, variable := range vars {
		if value, ok := s.model[variable]; ok {
			model[variable] = value
		}
	}
	return s.result, model, 0
}

func (*fixedSMTSolver) Stats() smt.Stats { return smt.Stats{} }

func (*fixedSMTSolver) Close() error { return nil }

func smtQueryForCacheTest(a *Analyzer, hyps, obligation string) (*smtTranslator, string) {
	tr := a.newSMTTranslator(nil)
	query := tr.factPreamble() + hyps + "(assert (not " + obligation + "))\n" + tr.counterexampleExprDefinitions()
	return tr, query
}

func TestSMTInMemoryCacheHitDoesNotStartSolver(t *testing.T) {
	for _, cached := range []smtQueryResult{
		{proven: true},
		{proven: false, ce: "x=1"},
	} {
		name := "declined"
		if cached.proven {
			name = "proven"
		}
		t.Run(name, func(t *testing.T) {
			a := &Analyzer{
				smtEnabled: true,
				smtBinary:  filepath.Join(t.TempDir(), "missing-z3"),
			}
			tr, query := smtQueryForCacheTest(a, "", "true")
			a.smtQueryCache = map[string]smtQueryResult{query: cached}

			proven, ce := a.smtCheckQuery(tr, "", "true")
			if proven != cached.proven || ce != cached.ce {
				t.Fatalf("cached result=(%v,%q), want (%v,%q)", proven, ce, cached.proven, cached.ce)
			}
			if a.smtSolver != nil || a.smtUnavailable {
				t.Fatalf("cache hit touched solver startup: solver=%v unavailable=%v", a.smtSolver, a.smtUnavailable)
			}
			if a.smtProofCacheInitialized {
				t.Fatal("in-memory hit initialized the persistent cache")
			}
			if !a.smtStats.Enabled || a.smtStats.Attempts != 1 || a.smtStats.CacheHits != 1 || a.smtStats.PersistentCacheHits != 0 {
				t.Fatalf("unexpected cache stats: %+v", a.smtStats)
			}
			if cached.proven && (a.smtStats.Proven != 1 || a.smtStats.SolverProven != 0) {
				t.Fatalf("cached proof counted as solver proof: %+v", a.smtStats)
			}
			if !cached.proven && a.smtStats.Declined != 1 {
				t.Fatalf("cached decline not counted: %+v", a.smtStats)
			}
		})
	}
}

func TestSMTPersistentCacheHitDoesNotStartSolver(t *testing.T) {
	a := &Analyzer{
		smtEnabled: true,
		smtBinary:  filepath.Join(t.TempDir(), "missing-z3"),
	}
	tr, query := smtQueryForCacheTest(a, "", "true")
	cache := &smtProofCache{directory: t.TempDir(), identity: "test identity"}
	cache.publish(query)
	a.smtProofCache = cache
	a.smtProofCacheInitialized = true

	proven, ce := a.smtCheckQuery(tr, "", "true")
	if !proven || ce != "" {
		t.Fatalf("persistent proof cache result=(%v,%q), want (true, empty)", proven, ce)
	}
	if a.smtSolver != nil || a.smtUnavailable {
		t.Fatalf("persistent hit touched solver startup: solver=%v unavailable=%v", a.smtSolver, a.smtUnavailable)
	}
	if !a.smtStats.Enabled || a.smtStats.Attempts != 1 || a.smtStats.CacheHits != 1 || a.smtStats.PersistentCacheHits != 1 || a.smtStats.Proven != 1 || a.smtStats.SolverProven != 0 {
		t.Fatalf("unexpected persistent cache stats: %+v", a.smtStats)
	}
}

func TestSMTRawQueryCacheHitDoesNotStartSolver(t *testing.T) {
	query := "(assert false)\n"
	a := &Analyzer{
		smtEnabled:    true,
		smtBinary:     filepath.Join(t.TempDir(), "missing-z3"),
		smtQueryCache: map[string]smtQueryResult{query: {proven: true}},
	}

	proven, ce := a.smtCheckRawQueryWithLogLabel(query, "requires-vacuity", "fn")
	if !proven || ce != "" {
		t.Fatalf("cached raw result=(%v,%q), want (true, empty)", proven, ce)
	}
	if a.smtSolver != nil || a.smtUnavailable {
		t.Fatalf("raw-query cache hit touched solver startup: solver=%v unavailable=%v", a.smtSolver, a.smtUnavailable)
	}
	if !a.smtStats.Enabled || a.smtStats.Attempts != 1 || a.smtStats.CacheHits != 1 || a.smtStats.SolverProven != 0 {
		t.Fatalf("unexpected raw-query cache stats: %+v", a.smtStats)
	}
}

func TestSMTRawSatDoesNotHideRegularCounterexample(t *testing.T) {
	a := &Analyzer{
		smtEnabled:               true,
		smtProofCacheInitialized: true,
		smtProofCache:            &smtProofCache{directory: t.TempDir(), identity: "raw-vc-collision"},
		smtSolver: &fixedSMTSolver{
			result: smt.Sat,
			model:  map[string]string{"v_x": "1"},
		},
	}
	tr := a.newSMTTranslator(nil)
	tr.decls["x"] = true
	obligation := "(= v_x 0)"
	query := tr.factPreamble() + "(assert (not " + obligation + "))\n" + tr.counterexampleExprDefinitions()

	proven, _ := a.smtCheckRawQueryWithLogLabel(query, "requires-vacuity", "fn")
	if proven {
		t.Fatal("satisfiable raw query was reported as proven")
	}
	if _, cached := a.smtQueryCache[query]; cached {
		t.Fatal("raw SAT decline without a model was cached under the regular VC query key")
	}

	proven, ce := a.smtCheckQuery(tr, "", obligation)
	if proven || ce != "x=1" {
		t.Fatalf("regular VC result=(%v,%q), want false with counterexample x=1", proven, ce)
	}
}

func TestSMTQueryCacheDoesNotOverrideDisabledOrUnavailableSolver(t *testing.T) {
	for _, tc := range []struct {
		name        string
		enabled     bool
		unavailable bool
	}{
		{name: "disabled", enabled: false},
		{name: "unavailable", enabled: true, unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Analyzer{
				smtEnabled:     tc.enabled,
				smtUnavailable: tc.unavailable,
			}
			if tc.unavailable {
				a.smtStats.Enabled = true // the earlier failed open already records this.
			}
			tr, query := smtQueryForCacheTest(a, "", "true")
			a.smtQueryCache = map[string]smtQueryResult{query: {proven: true}}

			proven, _ := a.smtCheckQuery(tr, "", "true")
			if proven || a.smtStats.Attempts != 0 || a.smtStats.CacheHits != 0 {
				t.Fatalf("cached result bypassed solver state: proven=%v stats=%+v", proven, a.smtStats)
			}
			if a.smtStats.Enabled != tc.unavailable {
				t.Fatalf("Enabled=%v, want %v", a.smtStats.Enabled, tc.unavailable)
			}
		})
	}
}
