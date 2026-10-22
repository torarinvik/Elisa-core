package semantic

import "testing"

func TestAffineBranchSnapshotOwnsPrivateState(t *testing.T) {
	parent := NewScope(nil)
	a := newAnalyzerWithScope(parent)
	key := affineValueKey{Path: "owner"}
	a.currentAffineValues = map[affineValueKey]affineValueState{key: {ConsumedBy: "entry"}}
	outer := a.analyzeBlockWithAffineClonePrepared(nil, NewScope(parent), func() {
		a.currentAffineValues[key] = affineValueState{ConsumedBy: "outer"}
		inner := a.analyzeBlockWithAffineClonePrepared(nil, NewScope(parent), func() {
			a.currentAffineValues[key] = affineValueState{ConsumedBy: "inner"}
		})
		if a.currentAffineValues[key].ConsumedBy != "outer" {
			t.Fatal("nested branch changed outer state")
		}
		inner.Affine[key] = affineValueState{ConsumedBy: "edited inner"}
		if a.currentAffineValues[key].ConsumedBy != "outer" {
			t.Fatal("snapshot aliases outer state")
		}
	})
	if a.currentAffineValues[key].ConsumedBy != "entry" {
		t.Fatal("branch changed parent state")
	}
	sibling := a.analyzeBlockWithAffineClonePrepared(nil, NewScope(parent), nil)
	outer.Affine[key] = affineValueState{ConsumedBy: "edited outer"}
	if a.currentAffineValues[key].ConsumedBy != "entry" || sibling.Affine[key].ConsumedBy != "entry" {
		t.Fatal("snapshot aliases parent or sibling")
	}
}

func BenchmarkAffineBranchSnapshot(b *testing.B) {
	parent := NewScope(nil)
	a := newAnalyzerWithScope(parent)
	a.currentAffineValues = make(map[affineValueKey]affineValueState, 128)
	for i := 0; i < 128; i++ {
		a.currentAffineValues[affineValueKey{Root: &Symbol{}}] = affineValueState{Uninitialized: true}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		snapshot := a.analyzeBlockWithAffineClonePrepared(nil, NewScope(parent), nil)
		affineCloneSink = snapshot.Affine
	}
}
