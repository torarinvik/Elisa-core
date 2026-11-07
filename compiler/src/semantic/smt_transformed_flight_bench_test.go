package semantic

import (
	"elisacore/src/smt"
	"strings"
	"testing"
)

func BenchmarkSMTTransformedUnprovedSerial(b *testing.B) {
	a := &Analyzer{smtProofCache: &smtProofCache{directory: b.TempDir(), identity: "bench"}}
	query := "(assert (" + strings.Repeat("x ", 2048) + "))"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		result, _ := a.smtCheckTransformedProof(query, func() (smt.Result, map[string]string) { return smt.Unknown, nil })
		if result != smt.Unknown {
			b.Fatal(result)
		}
	}
}
