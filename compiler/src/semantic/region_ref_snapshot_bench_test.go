package semantic

import (
	"testing"
)

func BenchmarkRegionRefReadOnlyBlock(b *testing.B) {
	scope := NewScope(nil)
	a := &Analyzer{currentScope: scope, currentRegionRefs: map[*Symbol]regionRefState{}}
	for i := 0; i < 128; i++ {
		a.currentRegionRefs[&Symbol{Kind: SymbolLocal}] = regionRefStateFromParamDependency(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.analyzeBlockWithRegionCloneCapture(nil, scope)
	}
}
