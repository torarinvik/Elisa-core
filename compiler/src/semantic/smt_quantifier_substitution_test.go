//go:build cgo

package semantic

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestSubstituteTypedIdentsQuantifierScopes(t *testing.T) {
	a := &Analyzer{}
	outer := &ast.Ident{Name: "i"}
	bound := &ast.Ident{Name: "i"}
	q := &ast.QuantifierExpr{
		Vars: []string{"i"},
		In:   outer,
		Body: bound,
	}
	replacement := &ast.IntLit{Value: "7"}

	got, ok := a.substituteTypedIdents(q, map[string]ast.Expr{"i": replacement}, 0)
	if !ok {
		t.Fatal("substitution should preserve a shadowing quantifier")
	}
	cloned, ok := got.(*ast.QuantifierExpr)
	if !ok || cloned == q {
		t.Fatalf("expected a cloned quantifier, got %T", got)
	}
	if cloned.In != replacement {
		t.Fatalf("outer substitution in quantifier source = %T, want replacement", cloned.In)
	}
	if cloned.Body != bound {
		t.Fatalf("substitution rewrote shadowed binder body: %#v", cloned.Body)
	}
}

func TestSubstituteTypedIdentsQuantifierRejectsCapture(t *testing.T) {
	a := &Analyzer{}
	q := &ast.QuantifierExpr{
		Vars: []string{"j"},
		Body: &ast.BinaryExpr{
			Op:    lexer.TOKEN_LT,
			Left:  &ast.Ident{Name: "i"},
			Right: &ast.Ident{Name: "j"},
		},
	}
	_, ok := a.substituteTypedIdents(q, map[string]ast.Expr{"i": &ast.Ident{Name: "j"}}, 0)
	if ok {
		t.Fatal("substitution must decline when it would capture a quantifier binder")
	}
}
