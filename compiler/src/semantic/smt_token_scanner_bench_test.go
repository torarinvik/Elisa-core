package semantic

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkSMTLocalTokenConsumers(b *testing.B) {
	var local, context strings.Builder
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&local, "(assert (= v_%d (+ v_%d 1)))\n", i, i+1)
		fmt.Fprintf(&context, "(assert (= v_%d (+ v_%d 1)))\n", i, i+1)
	}
	b.Run("local-slice", func(b *testing.B) {
		hyps := local.String()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if sliceImmutableLocalHypotheses(hyps, "(assert (> v_0 1))") == "" {
				b.Fatal("lost hypotheses")
			}
		}
	})
	b.Run("preamble", func(b *testing.B) {
		input := context.String()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if smtPreambleContext(input) != input {
				b.Fatal("changed preamble")
			}
		}
	})
	for _, width := range []int{4, 128} {
		b.Run(fmt.Sprintf("macro-%d", width), func(b *testing.B) {
			tr := &smtTranslator{boolDefs: map[string]string{}}
			for i := 0; i < 16; i++ {
				tr.boolDefs[fmt.Sprintf("b_%d", i)] = "(and " + strings.Repeat(fmt.Sprintf("b_%d ", i+1), width) + ")"
			}
			tr.boolDefs["b_16"] = "true"
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if !strings.HasSuffix(smtBoolMacroContext(tr, "b_0"), "true\n") {
					b.Fatal("lost macro dependency")
				}
			}
		})
	}

}
