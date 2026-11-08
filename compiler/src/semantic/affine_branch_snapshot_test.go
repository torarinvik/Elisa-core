package semantic

import (
	"elisacore/src/ast"
	"testing"
)

var affineCloneSink map[affineValueKey]affineValueState

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

func TestAffineSnapshotPathsKeepParentAndSiblingsIndependent(t *testing.T) {
	paths := []struct {
		name string
		run  func(*Analyzer, *Scope) affineFlowSnapshot
	}{
		{"block", func(a *Analyzer, s *Scope) affineFlowSnapshot {
			return a.analyzeBlockWithAffineClonePrepared(nil, s, nil)
		}},
		{"expression", func(a *Analyzer, s *Scope) affineFlowSnapshot {
			_, snapshot := a.analyzeExprInAffineScope(&ast.IntLit{Value: "1"}, s)
			return snapshot
		}},
		{"value", func(a *Analyzer, s *Scope) affineFlowSnapshot {
			_, snapshot := a.analyzeValueExprInAffineScope(&ast.IntLit{Value: "1"}, nil, s)
			return snapshot
		}},
		{"match", func(a *Analyzer, s *Scope) affineFlowSnapshot {
			_, snapshot := a.analyzeMatchExprArmBodyWithAffineSnapshot(nil, s)
			return snapshot
		}},
		{"sequence", func(a *Analyzer, s *Scope) affineFlowSnapshot {
			return a.analyzeSequenceRewriteArmBodyWithAffineSnapshot(nil, s)
		}},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			parent := NewScope(nil)
			a := newAnalyzerWithScope(parent)
			sym := &Symbol{Name: "owner"}
			key := affineValueKey{Root: sym}
			initial := &ast.IntLit{Value: "1"}
			a.currentAffineValues = map[affineValueKey]affineValueState{key: {ConsumedBy: "entry"}}
			a.currentValueBindings = map[*Symbol]ast.Expr{sym: initial}
			originalType := &BuiltinType{Name: "i32"}
			a.currentSpecializedValueTypes = map[*Symbol]Type{sym: originalType}
			a.currentFunctionValues = map[*Symbol]*FuncType{sym: {Name: "entry", Return: originalType}}
			a.currentBorrowedOwnerRefs = map[*Symbol]borrowedOwnerRefState{sym: {Fields: map[string]borrowedOwnerRefState{"field": {HasDirect: true, Direct: key}}}}
			first := path.run(a, NewScope(parent))
			second := path.run(a, NewScope(parent))
			first.Affine[key] = affineValueState{ConsumedBy: "edited"}
			first.ValueBindings[sym] = &ast.IntLit{Value: "2"}
			first.SpecializedValueTypes[sym] = &BuiltinType{Name: "bool"}
			first.FunctionValues[sym].Name = "edited"
			first.BorrowedOwnerRefs[sym].Fields["field"] = borrowedOwnerRefState{}
			if a.currentAffineValues[key].ConsumedBy != "entry" || second.Affine[key].ConsumedBy != "entry" {
				t.Fatal("affine state aliases another path")
			}
			if a.currentValueBindings[sym] != initial || second.ValueBindings[sym] != initial {
				t.Fatal("value bindings alias another path")
			}
			if a.currentSpecializedValueTypes[sym] != originalType || second.SpecializedValueTypes[sym] != originalType {
				t.Fatal("specialized type bindings alias another path")
			}
			if a.currentFunctionValues[sym].Name != "entry" || second.FunctionValues[sym].Name != "entry" {
				t.Fatal("function values alias another path")
			}
			if !a.currentBorrowedOwnerRefs[sym].Fields["field"].HasDirect || !second.BorrowedOwnerRefs[sym].Fields["field"].HasDirect {
				t.Fatal("borrowed owner references alias another path")
			}
		})
	}
}

func BenchmarkSpecializedBranchSnapshot(b *testing.B) {
	parent := NewScope(nil)
	a := newAnalyzerWithScope(parent)
	a.currentSpecializedValueTypes = make(map[*Symbol]Type, 128)
	typ := &BuiltinType{Name: "i32"}
	for i := 0; i < 128; i++ {
		a.currentSpecializedValueTypes[&Symbol{}] = typ
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		snapshot := a.analyzeBlockWithAffineClonePrepared(nil, NewScope(parent), nil)
		specializedBranchSink = snapshot.SpecializedValueTypes
	}
}

var specializedBranchSink map[*Symbol]Type

func TestBlockBranchSnapshotOwnsBorrowAndStorageViewFacts(t *testing.T) {
	parent := NewScope(nil)
	a := newAnalyzerWithScope(parent)
	sym := &Symbol{Name: "owner"}
	entry := borrowedOwnerRefState{HasDirect: true, Direct: affineValueKey{Root: sym}}
	a.currentBorrowedOwnerRefs = map[*Symbol]borrowedOwnerRefState{
		sym: {Fields: map[string]borrowedOwnerRefState{"field": entry}},
	}
	a.currentStorageViewDeps = map[*Symbol]storageViewDependencyState{
		sym: {Sources: []string{"entry"}},
	}
	a.currentAliasCarriers = map[string][]string{"carrier": {"entry"}}
	a.currentAliasCarrierFieldOverrides = map[string]map[string][]string{"carrier": {"field": {"entry"}}}
	first := a.analyzeBlockWithAffineClonePrepared(nil, NewScope(parent), nil)
	second := a.analyzeBlockWithAffineClonePrepared(nil, NewScope(parent), nil)
	first.BorrowedOwnerRefs[sym].Fields["field"] = borrowedOwnerRefState{}
	first.StorageViewDeps[sym].Sources[0] = "edited"
	first.AliasCarriers["carrier"][0] = "edited"
	first.AliasCarrierFieldOverrides["carrier"]["field"][0] = "edited"
	for _, facts := range []affineFlowSnapshot{
		second,
		{BorrowedOwnerRefs: a.currentBorrowedOwnerRefs, StorageViewDeps: a.currentStorageViewDeps, AliasCarriers: a.currentAliasCarriers, AliasCarrierFieldOverrides: a.currentAliasCarrierFieldOverrides},
	} {
		if facts.BorrowedOwnerRefs[sym].Fields["field"].Direct != entry.Direct ||
			facts.StorageViewDeps[sym].Sources[0] != "entry" ||
			facts.AliasCarriers["carrier"][0] != "entry" ||
			facts.AliasCarrierFieldOverrides["carrier"]["field"][0] != "entry" {
			t.Fatal("branch snapshot aliases another path")
		}
	}
}
