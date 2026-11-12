package ast

import "testing"

func TestCloneExprSubstTraversesExpressionWrappers(t *testing.T) {
	cases := []struct {
		name  string
		expr  Expr
		child func(Expr) Expr
	}{
		{"move", &MoveExpr{Operand: &Ident{Name: "x"}}, func(e Expr) Expr { return e.(*MoveExpr).Operand }},
		{"address", &AddrOfExpr{Operand: &Ident{Name: "x"}}, func(e Expr) Expr { return e.(*AddrOfExpr).Operand }},
		{"cast", &CastExpr{Operand: &Ident{Name: "x"}}, func(e Expr) Expr { return e.(*CastExpr).Operand }},
		{"specialization", &SpecializeExpr{Operand: &Ident{Name: "x"}}, func(e Expr) Expr { return e.(*SpecializeExpr).Operand }},
		{"slice object", &SliceExpr{Object: &Ident{Name: "x"}}, func(e Expr) Expr { return e.(*SliceExpr).Object }},
		{"slice start", &SliceExpr{Start: &Ident{Name: "x"}}, func(e Expr) Expr { return e.(*SliceExpr).Start }},
		{"slice end", &SliceExpr{End: &Ident{Name: "x"}}, func(e Expr) Expr { return e.(*SliceExpr).End }},
		{"tuple element", &TupleExpr{Elems: []Expr{&Ident{Name: "x"}}}, func(e Expr) Expr { return e.(*TupleExpr).Elems[0] }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			clone := CloneExprSubst(test.expr, map[string]Expr{"x": &Ident{Name: "replacement"}})
			if clone == nil || test.child(clone).(*Ident).Name != "replacement" {
				t.Fatalf("substitution failed: %#v", clone)
			}
			if test.child(test.expr).(*Ident).Name != "x" {
				t.Fatal("substitution modified original")
			}
		})
	}
}
