package ast

import "testing"

func TestCloneCallPreservesSourceMetadata(t *testing.T) {
	original := &CallExpr{Func: &Ident{Name: "function"}, SafeReceiver: &Ident{Name: "receiver"}, Args: []Expr{&Ident{Name: "argument"}}, ArgNames: []string{"named"}, ArgShorthand: []bool{true}, ArgItemOrder: []CallArgItem{{ArgIndex: 0}}, LmutRebindClaims: []LmutRebindClaim{{Name: "thread"}}, Safe: true, SafeConcurrencySugar: true, LmutThreadEffect: true, HasArgForward: true, FStringLowering: true}
	for _, substitute := range []bool{false, true} {
		var value Expr
		if substitute {
			value = CloneExprSubst(original, map[string]Expr{"receiver": &Ident{Name: "other"}, "argument": &Ident{Name: "replacement"}})
		} else {
			value = CloneExpr(original)
		}
		clone, ok := value.(*CallExpr)
		if !ok || !clone.Safe || !clone.SafeConcurrencySugar || !clone.LmutThreadEffect || !clone.HasArgForward || !clone.FStringLowering || len(clone.ArgNames) != 1 || len(clone.ArgShorthand) != 1 || len(clone.ArgItemOrder) != 1 || len(clone.LmutRebindClaims) != 1 {
			t.Fatalf("call metadata was lost: %#v", value)
		}
		if substitute && (clone.SafeReceiver.(*Ident).Name != "other" || clone.Args[0].(*Ident).Name != "replacement") {
			t.Fatal("substitution did not reach receiver and argument")
		}
		clone.LmutRebindClaims[0].Name = "changed"
		clone.ArgNames[0] = "changed"
		if original.LmutRebindClaims[0].Name != "thread" || original.ArgNames[0] != "named" {
			t.Fatal("call metadata aliases the original")
		}
	}
}
