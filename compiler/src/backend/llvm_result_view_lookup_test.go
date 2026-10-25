//go:build cgo

package backend

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
)

func TestSharedProofSpanIndexRequiresExactSemanticView(t *testing.T) {
	oldPos := lexer.Pos{File: "old-view.elisa", Line: 1, Col: 1, Offset: 1, EndOffset: 2}
	currentPos := lexer.Pos{File: "current-view.elisa", Line: 1, Col: 1, Offset: 1, EndOffset: 2}
	old := &semantic.Result{GlobalScope: &semantic.Scope{Symbols: map[string]*semantic.Symbol{}}, IndexBoundsProven: map[*ast.IndexExpr]bool{&ast.IndexExpr{Position: oldPos}: true}}
	current := &semantic.Result{GlobalScope: &semantic.Scope{Symbols: map[string]*semantic.Symbol{}}, IndexBoundsProven: map[*ast.IndexExpr]bool{&ast.IndexExpr{Position: currentPos}: true}}
	shared := newSharedCodegenLookups(old)
	g := &llvmGenerator{result: current, sharedLookups: shared, sharedResult: true}
	state := &functionState{g: g}
	if old.IndexProofSpanCount() != current.IndexProofSpanCount() {
		t.Fatal("fixture must have equal proof counts")
	}
	if state.indexBoundsProven(&ast.IndexExpr{Position: oldPos}) {
		t.Fatal("equal-sized foreign semantic view authorized an old span")
	}
	if !state.indexBoundsProven(&ast.IndexExpr{Position: currentPos}) {
		t.Fatal("current semantic view lost its rebuilt-node proof")
	}
}

func TestBackendViewFactLookupKeepsFalseMembership(t *testing.T) {
	node := &ast.BoolLit{Value: false}
	result := &semantic.Result{GhostContracts: map[ast.Expr]bool{node: false}}
	value, present := result.LookupGhostContracts(node)
	if !present || value {
		t.Fatal("false node-fact entry was confused with absence")
	}
	if result.ValueGhostContracts(node) {
		t.Fatal("single-value lookup changed zero-value semantics")
	}
	if _, present := result.LookupGhostContracts(&ast.BoolLit{Value: false}); present {
		t.Fatal("lookup used source/value equality instead of node identity")
	}
}
