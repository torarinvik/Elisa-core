package semantic

import (
	"sync"
	"testing"
)

func BenchmarkSMTCompletedFlightHit(b *testing.B) {
	flights := &sync.Map{}
	a := &Analyzer{parallel: &parallelWorkerState{smtFlights: flights}}
	query := "(assert false)"
	_, _, flight := a.awaitSharedSMTQuery(query)
	if flight == nil {
		b.Fatal("missing leader")
	}
	a.resolveSMTFlight(query, smtQueryResult{proven: true})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, ok, owner := a.awaitSharedSMTQuery(query)
		if !ok || !result.proven || owner != nil {
			b.Fatal("missed proof")
		}
	}
}
