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
			first := path.run(a, NewScope(parent))
			second := path.run(a, NewScope(parent))
			first.Affine[key] = affineValueState{ConsumedBy: "edited"}
			first.ValueBindings[sym] = &ast.IntLit{Value: "2"}
			if a.currentAffineValues[key].ConsumedBy != "entry" || second.Affine[key].ConsumedBy != "entry" {
				t.Fatal("affine state aliases another path")
			}
			if a.currentValueBindings[sym] != initial || second.ValueBindings[sym] != initial {
				t.Fatal("value bindings alias another path")
			}
		})
	}
}
