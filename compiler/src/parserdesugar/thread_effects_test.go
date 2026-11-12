package parserdesugar

import (
	"elisacore/src/ast"
	"testing"
)

func TestThreadEffectReceiverChain(t *testing.T) {
	root := &ast.Ident{Name: "parser"}
	inner := &ast.CallExpr{Func: &ast.FieldExpr{Object: root, Field: "advance"}}
	outer := &ast.CallExpr{Func: &ast.FieldExpr{Object: inner, Field: "skip"}}
	if name, ok := ExprRootIdentName(outer); !ok || name != "parser" {
		t.Fatalf("root %q %v", name, ok)
	}
	MarkThreadEffect(outer)
	if !inner.LmutThreadEffect || !outer.LmutThreadEffect {
		t.Fatal("chain was not marked")
	}
	if _, ok := ExprRootIdentName(&ast.CallExpr{Func: &ast.Ident{Name: "call"}}); ok {
		t.Fatal("plain call has no receiver root")
	}
}
