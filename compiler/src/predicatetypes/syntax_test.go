package predicatetypes

import (
	"elisacore/src/ast"
	"testing"
)

func TestPredicateSyntaxChecks(t *testing.T) {
	r := &ast.MembershipRangeExpr{}
	wrapper := &ast.ListLitExpr{Elems: []ast.Expr{r}}
	if got, list, ok := MembershipRange(wrapper); !ok || got != r || list != wrapper {
		t.Fatal("range wrapper must preserve current nodes")
	}
	if _, _, ok := MembershipRange(&ast.ListLitExpr{Elems: []ast.Expr{r, r}}); ok {
		t.Fatal("multiple candidates must not be treated as a direct range")
	}
	if !TypingMatchesNodes([]ast.Expr{&ast.BoolLit{}, r}, "bR") || TypingMatchesNodes([]ast.Expr{&ast.BoolLit{}, r}, "bn") {
		t.Fatal("range tag must match syntax")
	}
	if BuiltinSyntaxName(&ast.NamedType{Name: "i64"}) != "i64" || BuiltinSyntaxName(&ast.NamedType{Name: "User"}) != "" {
		t.Fatal("only canonical scalar types are admitted")
	}
}
