package semantic

import (
	"elisacore/src/smt"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

type parallelSMTStatsSolver struct {
	stats  smt.Stats
	closes int
}

func (s *parallelSMTStatsSolver) Check(string) (smt.Result, time.Duration) { return smt.Unsat, 0 }
func (s *parallelSMTStatsSolver) CheckValues(string, []string) (smt.Result, map[string]string, time.Duration) {
	return smt.Unsat, nil, 0
}
func (s *parallelSMTStatsSolver) Stats() smt.Stats { return s.stats }
func (s *parallelSMTStatsSolver) Close() error     { s.closes++; return nil }

func TestParallelSMTWorkerOwnsSolverAndProfile(t *testing.T) {
	parentSolver := &parallelSMTStatsSolver{stats: smt.Stats{Total: 5 * time.Millisecond, Slowest: 4 * time.Millisecond}}
	parent := &Analyzer{smtEnabled: true, smtSolver: parentSolver, smtStats: SMTStats{Enabled: true, Attempts: 7, Proven: 7, SpawnTime: time.Millisecond}}
	worker := cloneAnalyzerValueForBodyWorker(parent).Addr().Interface().(*Analyzer)
	if worker.smtSolver != nil || worker.smtStats != (SMTStats{}) {
		t.Fatal("worker inherited live solver or parent counters")
	}
	own := &parallelSMTStatsSolver{stats: smt.Stats{Total: 3 * time.Millisecond, Slowest: 2 * time.Millisecond}}
	worker.smtSolver = own
	worker.smtStats = SMTStats{Enabled: true, Attempts: 2, Proven: 1, Declined: 1, SolverProven: 1, CacheHits: 1, PersistentCacheHits: 1, SpawnTime: 2 * time.Millisecond}
	worker.closeSMT()
	worker.closeSMT()
	worker.smtUnavailable = true
	worker.smtSnapshotTainted = true
	if own.closes != 1 || parentSolver.closes != 0 {
		t.Fatal("worker closed wrong solver or closed twice")
	}
	// Reflection merging must not overwrite the parent's solver or add profile
	// deltas against a pre-fork baseline. Profiles are folded explicitly once.
	baseline := cloneAnalyzerValueForParallel(parent)
	cleanWorker := cloneAnalyzerValueForBodyWorker(parent).Addr().Interface().(*Analyzer)
	mergeValue(settable(reflect.ValueOf(parent).Elem()), reflect.ValueOf(worker).Elem(), baseline, &parallelWorkerState{})
	mergeParallelSMTStats(&parent.smtStats, worker.smtStats)
	mergeValue(settable(reflect.ValueOf(parent).Elem()), reflect.ValueOf(cleanWorker).Elem(), baseline, &parallelWorkerState{})
	if parent.smtSolver != parentSolver || parent.smtUnavailable || !parent.smtSnapshotTainted {
		t.Fatal("worker merge changed parent solver or cleared sticky snapshot taint")
	}
	parent.closeSMT()
	parent.closeSMT()
	want := SMTStats{Enabled: true, Attempts: 9, Proven: 8, Declined: 1, SolverProven: 1, CacheHits: 1, PersistentCacheHits: 1, SpawnTime: 3 * time.Millisecond, SolverTime: 8 * time.Millisecond, Slowest: 4 * time.Millisecond}
	if parent.smtStats != want {
		t.Fatalf("profile got %+v want %+v", parent.smtStats, want)
	}
	if parentSolver.closes != 1 {
		t.Fatal("parent solver lifecycle changed")
	}
}

func TestParallelSMTIndependentSessions(t *testing.T) {
	if _, err := exec.LookPath(smt.ResolveBinary("")); err != nil {
		t.Skip("solver unavailable")
	}
	parent := &Analyzer{smtEnabled: true}
	root := parent.openSMT()
	if root == nil {
		t.Fatal("missing parent solver")
	}
	defer parent.closeSMT()
	results := make(chan string, 2)
	for _, value := range []int{11, 22} {
		worker := cloneAnalyzerValueForBodyWorker(parent).Addr().Interface().(*Analyzer)
		own := worker.openSMT()
		if own == nil || own == root {
			t.Fatal("worker shares parent session")
		}
		go func(worker *Analyzer, value int) {
			message := ""
			defer func() { worker.closeSMT(); results <- message }()
			query := fmt.Sprintf("(declare-const x Int)\n(assert (= x %d))\n", value)
			verdict, model, _ := worker.smtSolver.CheckValues(query, []string{"x"})
			if verdict != smt.Sat || model["x"] != fmt.Sprint(value) {
				message = fmt.Sprintf("wrong worker model: %v %v", verdict, model)
				return
			}
		}(worker, value)
	}
	for range 2 {
		if err := <-results; err != "" {
			t.Fatal(err)
		}
	}
	if verdict, _ := root.Check("(assert false)\n"); verdict != smt.Unsat {
		t.Fatal("worker shutdown affected parent")
	}
}

func TestParallelSMTStrictProofsMatchSerial(t *testing.T) {
	if _, err := exec.LookPath(smt.ResolveBinary("")); err != nil {
		t.Skip("solver unavailable")
	}
	t.Setenv("ELISACORE_SMT_CACHE_DIR", "")
	var src strings.Builder
	src.WriteString("law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi\ntype Small = i64 is Bounded[2, 10]\n")
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&src, "def mul%d(a: Small, b: Small) -> i64 is Bounded[%d, %d]:\n    return a * b + %d\n", i, 4+i, 100+i, i)
	}
	for _, workers := range []string{"1", "2", "4"} {
		t.Setenv("ELISA_PARALLEL_BODIES", workers)
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "parallel_smt.elisa", src.String(), AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
		if len(result.Errors()) != 0 {
			t.Fatalf("workers %s: %v", workers, result.Errors())
		}
		proven := 0
		for _, proof := range result.ProofReport {
			if proof.Outcome == ProofProvenSMT {
				proven++
			}
		}
		if proven != 12 || result.SMTProfile.Proven != 24 || result.SMTProfile.Declined != 0 {
			t.Fatalf("workers %s proofs=%d stats=%+v", workers, proven, result.SMTProfile)
		}
		if result.SMTProfile.SolverTime <= 0 || result.SMTProfile.SpawnTime <= 0 {
			t.Fatalf("workers %s lost solver time", workers)
		}
	}
}
