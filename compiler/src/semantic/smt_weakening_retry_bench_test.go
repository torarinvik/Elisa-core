package semantic

import (
	"elisacore/src/smt"
	"strings"
	"testing"
)

func BenchmarkSMTDefinitiveWeakening(b *testing.B) {
	goal := "(assert (not (>= x 0)))"
	query := "(declare-const x Int)\n(declare-fun p (Int) Bool)\n" + strings.Repeat("(assert (forall ((i Int)) (p i)))\n", 128) + goal
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		solver := &weakeningRetrySolver{first: smt.Sat}
		if got, _ := (&Analyzer{}).smtCheckSliced(solver, query, goal, nil); got != smt.Unknown {
			b.Fatal(got)
		}
	}
}
