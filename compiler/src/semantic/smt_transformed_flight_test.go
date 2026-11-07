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
