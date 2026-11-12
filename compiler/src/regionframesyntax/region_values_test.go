package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestUnwrapRegionValuePreservesBacking(t *testing.T) {
	value := &ast.Ident{Name: "value"}
	expr := &ast.ParenExpr{Inner: &ast.MoveExpr{Operand: &ast.CanExpr{Expr: value}}}
	if got := UnwrapRegionValue(expr); got != value {
		t.Fatalf("got %T", got)
	}
	if got := SimpleTypeNameSegment("module::nested.Type"); got != "Type" {
		t.Fatalf("got %q", got)
	}
}
