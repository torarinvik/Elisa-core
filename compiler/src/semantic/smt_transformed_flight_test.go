package semantic

import (
	"elisacore/src/smt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSMTTransformedFlightSharesOnlyProofs(t *testing.T) {
	shared := &sync.Map{}
	dir := t.TempDir()
	makeAnalyzer := func(identity string) *Analyzer {
		return &Analyzer{smtProofCache: &smtProofCache{directory: dir, identity: identity}, parallel: &parallelWorkerState{smtFlights: shared}}
	}
	leader, follower := makeAnalyzer("same"), makeAnalyzer("same")
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	result := make(chan smt.Result, 2)
	go func() {
		r, _ := leader.smtCheckTransformedProof("(assert false)", func() (smt.Result, map[string]string) {
			calls.Add(1)
			close(entered)
			<-release
			return smt.Unsat, nil
		})
		result <- r
	}()
	<-entered
	go func() {
		r, _ := follower.smtCheckTransformedProof("(assert false)", func() (smt.Result, map[string]string) {
			calls.Add(1)
			return smt.Unsat, nil
		})
		result <- r
	}()
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case got := <-result:
			if got != smt.Unsat {
				t.Fatal(got)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("flight did not finish")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("proof solved %d times", calls.Load())
	}
	isolated := makeAnalyzer("different")
	r, _ := isolated.smtCheckTransformedProof("(assert false)", func() (smt.Result, map[string]string) { calls.Add(1); return smt.Unknown, nil })
	if r != smt.Unknown || calls.Load() != 2 {
		t.Fatal("proof crossed solver identities")
	}
	proofA, proofB := makeAnalyzer("same-base"), makeAnalyzer("same-base")
	proofA.smtProofCache.proofIdentity = "solver-one"
	proofB.smtProofCache.proofIdentity = "solver-two"
	_, _ = proofA.smtCheckTransformedProof("(assert false)", func() (smt.Result, map[string]string) { return smt.Unsat, nil })
	distinctChecks := 0
	r, _ = proofB.smtCheckTransformedProof("(assert false)", func() (smt.Result, map[string]string) { distinctChecks++; return smt.Unknown, nil })
	if r != smt.Unknown || distinctChecks != 1 {
		t.Fatal("proof crossed proof identities")
	}
	for _, verdict := range []smt.Result{smt.Sat, smt.Unknown} {
		a, b := makeAnalyzer("nonproof"), makeAnalyzer("nonproof")
		checks := 0
		for _, worker := range []*Analyzer{a, b} {
			r, model := worker.smtCheckTransformedProof("(assert true)", func() (smt.Result, map[string]string) { checks++; return verdict, map[string]string{"x": "1"} })
			if r != verdict || model["x"] != "1" {
				t.Fatal("non-proof result changed")
			}
		}
		if checks != 2 {
			t.Fatal("non-proof result was shared")
		}
	}
}

func TestSMTTransformedReentrantCallDoesNotReleaseOwner(t *testing.T) {
	a := &Analyzer{smtProofCache: &smtProofCache{directory: t.TempDir(), identity: "same"}, parallel: &parallelWorkerState{smtFlights: &sync.Map{}}}
	query := "(assert false)"
	_, _ = a.smtCheckTransformedProof(query, func() (smt.Result, map[string]string) {
		key := "\x00transformed-unsat\x00" + a.smtProofCache.key(query)
		outer := a.parallel.leading[key]
		_, _ = a.smtCheckTransformedProof(query, func() (smt.Result, map[string]string) { return smt.Unsat, nil })
		if a.parallel.leading[key] != outer || outer == nil {
			t.Fatal("nested call released owner")
		}
		select {
		case <-outer.done:
			t.Fatal("nested call closed flight")
		default:
		}
		return smt.Unknown, nil
	})
}

func TestSMTTransformedFlightSharesAlphaEquivalentProof(t *testing.T) {
	shared := &sync.Map{}
	dir := t.TempDir()
	cache := func() *smtProofCache { return &smtProofCache{directory: dir, identity: "same"} }
	first := &Analyzer{smtProofCache: cache(), parallel: &parallelWorkerState{smtFlights: shared}}
	second := &Analyzer{smtProofCache: cache(), parallel: &parallelWorkerState{smtFlights: shared}}
	q1 := "(declare-const v_old Int)\n(assert (< v_old v_old))\n"
	q2 := "(declare-const v_new Int)\n(assert (< v_new v_new))\n"
	k1, ok1 := first.smtProofCache.alphaKey(q1)
	k2, ok2 := second.smtProofCache.alphaKey(q2)
	if !ok1 || !ok2 || k1 != k2 {
		t.Fatal("fixture is not alpha-equivalent")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	results := make(chan smt.Result, 2)
	var calls atomic.Int32
	go func() {
		r, _ := first.smtCheckTransformedProof(q1, func() (smt.Result, map[string]string) { calls.Add(1); close(entered); <-release; return smt.Unsat, nil })
		results <- r
	}()
	<-entered
	started := make(chan struct{})
	go func() {
		close(started)
		r, _ := second.smtCheckTransformedProof(q2, func() (smt.Result, map[string]string) { calls.Add(1); return smt.Unsat, nil })
		results <- r
	}()
	<-started
	select {
	case r := <-results:
		t.Fatalf("follower solved before proof completed: %v", r)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r != smt.Unsat {
				t.Fatal(r)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("flight did not complete")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("alpha-equivalent proof solved %d times", calls.Load())
	}
}

func TestSMTWeakeningFlightSharesSATWithoutModels(t *testing.T) {
	shared := &sync.Map{}
	dir := t.TempDir()
	makeAnalyzer := func() *Analyzer {
		return &Analyzer{smtProofCache: &smtProofCache{directory: dir, identity: "same"}, parallel: &parallelWorkerState{smtFlights: shared}}
	}
	first, second := makeAnalyzer(), makeAnalyzer()
	q1 := "(declare-const v_old Int)\n(assert (> v_old 0))\n"
	q2 := "(declare-const v_new Int)\n(assert (> v_new 0))\n"
	k1, ok1 := first.smtProofCache.alphaKey(q1)
	k2, ok2 := second.smtProofCache.alphaKey(q2)
	if !ok1 || !ok2 || k1 != k2 {
		t.Fatal("fixture is not alpha-equivalent")
	}
	calls := 0
	result, _ := first.smtCheckTransformedVerdict(q1, true, func() (smt.Result, map[string]string) {
		calls++
		return smt.Sat, map[string]string{"v_old": "1"}
	})
	if result != smt.Sat {
		t.Fatal(result)
	}
	result, model := second.smtCheckTransformedVerdict(q2, true, func() (smt.Result, map[string]string) {
		calls++
		return smt.Unknown, nil
	})
	if result != smt.Sat || model != nil || calls != 1 {
		t.Fatalf("shared verdict=%v model=%v calls=%d", result, model, calls)
	}
	normal := makeAnalyzer()
	result, _ = normal.smtCheckTransformedProof(q2, func() (smt.Result, map[string]string) { calls++; return smt.Unknown, nil })
	if result != smt.Unknown || calls != 2 {
		t.Fatal("model-consuming path reused SAT")
	}
	for _, worker := range []*Analyzer{makeAnalyzer(), makeAnalyzer()} {
		result, _ = worker.smtCheckTransformedVerdict("(assert unknown)", true, func() (smt.Result, map[string]string) { calls++; return smt.Unknown, nil })
		if result != smt.Unknown {
			t.Fatal("unknown changed")
		}
	}
	if calls != 4 {
		t.Fatal("unknown was shared")
	}
}
