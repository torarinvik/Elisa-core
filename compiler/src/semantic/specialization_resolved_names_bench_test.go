package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"testing"
)

var specializationResolvedNamesSink *SpecializedFunction

// Measure construction rather than shared memo hits, while scaling only facts
// unrelated to the specialized function. Each product must retain their logical view.
func BenchmarkSpecializationResolvedNames(b *testing.B) {
	for _, rows := range []int{0, 16384} {
		b.Run(fmt.Sprintf("unrelated_%d", rows), func(b *testing.B) {
			root, fn, args := specializationBenchmarkAnalyze(b, specializationBenchmarkSource(0))
			for i := 0; i < rows; i++ {
				expr := &ast.NamedType{Name: fmt.Sprintf("external%d", i)}
				root.ResolvedTypeNames[expr] = "Canonical.External"
			}
			root.BeginSharedCodegen()
			defer root.EndSharedCodegen()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				product, err := root.specializePrivateFunction(fn, args)
				if err != nil {
					b.Fatal(err)
				}
				specializationResolvedNamesSink = product
			}
		})
	}
}
