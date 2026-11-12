package lemmasubstitution

import (
	"elisacore/src/ast"
	"testing"
)

func TestSubstitutionAndCapture(t *testing.T) {
	argument := &ast.IntLit{Value: "4"}
	contains := func(expr ast.Expr, name string) bool { id, ok := expr.(*ast.Ident); return ok && id.Name == name }
	work := 32
	got, ok := WithBudget(&ast.Ident{Name: "x"}, map[string]ast.Expr{"x": argument}, &work, 0, contains)
	if !ok || got != argument {
		t.Fatal("parameter substitution failed")
	}
	quantifier := &ast.QuantifierExpr{Vars: []string{"bound"}, Body: &ast.Ident{Name: "x"}}
	work = 32
	if _, ok := WithBudget(quantifier, map[string]ast.Expr{"x": &ast.Ident{Name: "bound"}}, &work, 0, contains); ok {
		t.Fatal("accepted captured variable")
	}
	work = 0
	if _, ok := WithBudget(argument, nil, &work, 0, contains); ok {
		t.Fatal("accepted exhausted budget")
	}
}

func TestDepthLimit(t *testing.T) {
	var expr ast.Expr = &ast.IntLit{Value: "1"}
	for i := 0; i < MaxDepth; i++ {
		expr = &ast.ParenExpr{Inner: expr}
	}
	work := 8192
	if _, ok := WithBudget(expr, nil, &work, 0, nil); ok {
		t.Fatal("accepted expression beyond depth limit")
	}
}
