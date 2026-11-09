package semantic

import (
	"fmt"
	"testing"
)

var affineCloneBenchSink map[affineValueKey]affineValueState

func BenchmarkCloneAffineValueStates(b *testing.B) {
	for _, n := range []int{4, 16, 64} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			a := &Analyzer{analyzerFunctionRegionState: analyzerFunctionRegionState{
				currentAffineValues: make(map[affineValueKey]affineValueState, n),
			}}
			for i := 0; i < n; i++ {
				a.currentAffineValues[affineValueKey{Root: &Symbol{Name: fmt.Sprint(i)}}] = affineValueState{ConsumedBy: "moved"}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				affineCloneBenchSink = a.cloneAffineValueStates()
			}
		})
	}
}
