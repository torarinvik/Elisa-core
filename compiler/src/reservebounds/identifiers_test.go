package reservebounds

import (
	"elisacore/src/ast"
	"testing"
)

func TestIdentifiersResolvableChecksNestedNames(t *testing.T) {
	expr := &ast.BinaryExpr{Left: &ast.Ident{Name: "known"}, Right: &ast.ParenExpr{Inner: &ast.Ident{Name: "missing"}}}
	lookup := func(name string) bool { return name == "known" }
	if IdentifiersResolvable(expr, lookup) {
		t.Fatal("unknown nested identifier was accepted")
	}
	if !IdentifiersResolvable(&ast.Ident{Name: "known"}, lookup) || !IdentifiersResolvable(nil, lookup) {
		t.Fatal("known or empty expression was rejected")
	}
}
