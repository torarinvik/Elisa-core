package semantic

import "testing"

func BenchmarkRegionStateBlockCapture(b *testing.B) {
	scope := NewScope(nil)
	a := &Analyzer{currentScope: scope, currentRegions: map[*Symbol]regionState{}}
	for i := 0; i < 128; i++ {
		a.currentRegions[&Symbol{Kind: SymbolLocal}] = regionState{Generation: i}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.analyzeBlockWithRegionCloneCapture(nil, scope)
	}
}

func TestRegionStateCaptureKeepsCallerAndBranchIsolated(t *testing.T) {
	sym := &Symbol{Name: "region"}
	scope := NewScope(nil)
	caller := map[*Symbol]regionState{sym: {Generation: 1}}
	a := &Analyzer{currentScope: scope, currentRegions: caller}
	branch := a.analyzeBlockWithRegionCloneCapture(nil, scope)
	branch[sym] = regionState{Generation: 1, Destroyed: true}
	if caller[sym].Destroyed {
		t.Fatal("branch changed caller")
	}
	caller[sym] = regionState{Generation: 2}
	if branch[sym].Generation != 1 {
		t.Fatal("caller changed captured branch")
	}
}

func BenchmarkRegionStateReadOnlyBlock(b *testing.B) {
	scope := NewScope(nil)
	a := &Analyzer{currentScope: scope, currentRegions: map[*Symbol]regionState{}}
	for i := 0; i < 128; i++ {
		a.currentRegions[&Symbol{Kind: SymbolLocal}] = regionState{Generation: i}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.analyzeBlockWithRegionClone(nil, scope)
	}
}
