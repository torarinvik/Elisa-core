package exprpurity

import (
	"elisacore/src/ast"
	"testing"
)

func TestPurityChecksPreserveDifferentWhitelists(t *testing.T) {
	literal := &ast.IntLit{Value: "1"}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "f"}}
	overloaded := &ast.BinaryExpr{Left: literal, Right: literal, LoweredCall: call}
	index := &ast.IndexExpr{Object: &ast.Ident{Name: "xs"}, Index: literal}
	for _, tc := range []struct {
		expr          ast.Expr
		where, reeval bool
	}{
		{literal, true, true}, {call, false, false}, {overloaded, true, false}, {index, true, false},
		{&ast.ListLitExpr{Elems: []ast.Expr{literal}, Keys: []ast.Expr{call}}, false, false},
		{&ast.StructLitExpr{CopyBase: &ast.Ident{Name: "source"}}, false, false},
	} {
		if WherePredicate(tc.expr) != tc.where || Reevaluatable(tc.expr, 0) != tc.reeval {
			t.Fatalf("unexpected purity for %T", tc.expr)
		}
	}
	if Reevaluatable(literal, 65) {
		t.Fatal("depth limit must be enforced")
	}
}

func TestTerminationRejectsEffectfulAndGuardedForms(t *testing.T) {
	name := &ast.Ident{Name: "xs"}
	index := &ast.IndexExpr{Object: name, Index: &ast.IntLit{Value: "1"}}
	if !Termination(index) {
		t.Fatal("ordinary indexed read should be admitted")
	}
	index.Fallback = &ast.IntLit{Value: "0"}
	if Termination(index) {
		t.Fatal("fallback indexing must be declined")
	}
	if Termination(&ast.FieldExpr{Object: name, Field: "x", Safe: true}) {
		t.Fatal("safe projection must be declined")
	}
	if Termination(&ast.CallExpr{Func: name}) {
		t.Fatal("calls must be declined")
	}
}
