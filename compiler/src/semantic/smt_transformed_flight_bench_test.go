package semantic

import (
	"elisacore/src/smt"
	"strings"
	"sync"
	"testing"
)

func BenchmarkSMTTransformedUnprovedSerial(b *testing.B) {
	a := &Analyzer{analyzerProofSolverState: analyzerProofSolverState{
		smtProofCache: &smtProofCache{directory: b.TempDir(), identity: "bench"},
	}}
	query := "(assert (" + strings.Repeat("x ", 2048) + "))"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		result, _ := a.smtCheckTransformedProof(query, func() (smt.Result, map[string]string) { return smt.Unknown, nil })
		if result != smt.Unknown {
			b.Fatal(result)
		}
	}
}

func BenchmarkSMTTransformedRepeatedModelFree(b *testing.B) {
	query := "(declare-const v_x Int)" + strings.Repeat("(assert (>= v_x 0))", 1000)
	flights := &sync.Map{}
	worker := func() *Analyzer {
		return &Analyzer{analyzerProofSolverState: analyzerProofSolverState{
			smtProofCache: &smtProofCache{directory: b.TempDir(), identity: "bench"},
		},
			parallel: &parallelWorkerState{smtFlights: flights}}
	}
	// Keep both workers in the same cache namespace and directory.
	first, next := worker(), worker()
	next.smtProofCache.directory = first.smtProofCache.directory
	first.smtCheckTransformedVerdict(query, true, func() (smt.Result, map[string]string) { return smt.Sat, nil })
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, _ := next.smtCheckTransformedVerdict(query, true, func() (smt.Result, map[string]string) { b.Fatal("shared result missed"); return smt.Unknown, nil })
		if result != smt.Sat {
			b.Fatal(result)
		}
	}
}
