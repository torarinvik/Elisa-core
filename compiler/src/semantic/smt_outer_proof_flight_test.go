package semantic

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"elisacore/src/smt"
)

func TestSMTOuterProofFlightSharesOnlyUnsat(t *testing.T) {
	flights := &sync.Map{}
	cache := &smtProofCache{directory: t.TempDir(), identity: "solver"}
	worker := func() *Analyzer { return &Analyzer{parallel: &parallelWorkerState{smtFlights: flights}} }
	q1 := "(declare-const v_a Int)(assert (< v_a v_a))"
	q2 := "(declare-const v_b Int)(assert (< v_b v_b))"
	k1, _ := cache.lookupProof(q1)
	k2, _ := cache.lookupProof(q2)
	if k1.alpha == "" || k1.alpha != k2.alpha {
		t.Fatal("queries must be alpha equivalent")
	}
	calls := 0
	first := worker()
	r, _, shared := first.smtShareOuterProof(k1, func() (smt.Result, map[string]string) { calls++; return smt.Unsat, nil })
	if r != smt.Unsat || shared {
		t.Fatal("leader did not solve")
	}
	second := worker()
	r, model, shared := second.smtShareOuterProof(k2, func() (smt.Result, map[string]string) { calls++; return smt.Unknown, nil })
	if r != smt.Unsat || model != nil || !shared || calls != 1 || second.smtStats.CacheHits != 1 {
		t.Fatal("proof was not shared")
	}
	for _, verdict := range []smt.Result{smt.Sat, smt.Unknown} {
		key := smtProofKeys{exact: verdict.String()}
		for i := 0; i < 2; i++ {
			r, model, shared := worker().smtShareOuterProof(key, func() (smt.Result, map[string]string) { return verdict, map[string]string{"local": "1"} })
			if r != verdict || shared || model["local"] != "1" {
				t.Fatal("non-proof result shared or changed")
			}
		}
	}
}

func TestSMTOuterProofConcurrentFlight(t *testing.T) {
	flights := &sync.Map{}
	worker := func() *Analyzer { return &Analyzer{parallel: &parallelWorkerState{smtFlights: flights}} }
	key := smtProofKeys{alpha: "same"}
	entered, release, results := make(chan struct{}), make(chan struct{}), make(chan bool, 2)
	var calls atomic.Int32
	go func() {
		_, _, shared := worker().smtShareOuterProof(key, func() (smt.Result, map[string]string) {
			calls.Add(1)
			close(entered)
			<-release
			return smt.Unsat, nil
		})
		results <- shared
	}()
	<-entered
	go func() {
		_, _, shared := worker().smtShareOuterProof(key, func() (smt.Result, map[string]string) { calls.Add(1); return smt.Unsat, nil })
		results <- shared
	}()
	close(release)
	sharedCount := 0
	for i := 0; i < 2; i++ {
		select {
		case shared := <-results:
			if shared {
				sharedCount++
			}
		case <-time.After(3 * time.Second):
			t.Fatal("flight did not finish")
		}
	}
	if calls.Load() != 1 || sharedCount != 1 {
		t.Fatalf("calls=%d shared=%d", calls.Load(), sharedCount)
	}
}
