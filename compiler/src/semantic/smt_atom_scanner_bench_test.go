package semantic

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkSMTQueryAtomConsumers(b *testing.B) {
	var text strings.Builder
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&text, "(declare-const x%d Int)\n", i)
	}
	for i := 0; i < 256; i++ {
		fmt.Fprintf(&text, "(assert (= (+ x%d x%d 1 2 3 4 5 6 7 8 9) 100))\n", i%127, (i+1)%127)
	}
	goal := "(assert (not (> x127 0)))"
	text.WriteString(goal)
	query := text.String()
	b.Run("slice", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _, ok := smtSliceQuery(query, goal)
			if !ok {
				b.Fatal("slice declined")
			}
		}
	})
	b.Run("head", func(b *testing.B) {
		command := "(assert (= (+ x0 x1 1 2 3 4 5 6 7 8 9) 100))"
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if smtCommandHead(command) != "assert" {
				b.Fatal("head changed")
			}
		}
	})
	b.Run("quantifier-check", func(b *testing.B) {
		// Balanced independent quantified hypothesis, followed by the unchanged goal.
		quantified := "(declare-const x127 Int)\n(assert (forall ((z Int)) (= z z)))\n" + goal
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, ok := smtWithoutQuantifiedHypotheses(quantified, goal)
			if !ok {
				b.Fatal("weakening declined")
			}
		}
	})
}
