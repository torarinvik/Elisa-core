package reservebounds

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestReserveBoundsDeclineUnknownExpressionIdentity(t *testing.T) {
	unknownA := &ast.MatchExpr{}
	unknownB := &ast.MatchExpr{}
	if ReserveBoundAtLeast(unknownA, unknownB) {
		t.Fatal("unsupported expressions were treated as equal")
	}
	fieldA := &ast.FieldExpr{Object: unknownA, Field: "count"}
	fieldB := &ast.FieldExpr{Object: unknownB, Field: "count"}
	if ReserveBoundAtLeast(fieldA, fieldB) {
		t.Fatal("unsupported field receivers were treated as equal")
	}
	if sum, ok := AddReserveExpr(unknownA, unknownB, lexer.Pos{}).(*ast.BinaryExpr); !ok || sum.Op != lexer.TOKEN_PLUS {
		t.Fatal("unknown expressions were incorrectly collapsed")
	}
}

func TestReserveBoundsRecognizeKnownLinearRelationships(t *testing.T) {
	name := func() ast.Expr { return &ast.Ident{Name: "n"} }
	one := &ast.IntLit{Value: "1"}
	larger := &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: name(), Right: one}
	if !ReserveBoundAtLeast(larger, name()) {
		t.Fatal("n + 1 should cover n")
	}
	if ReserveBoundAtLeast(name(), larger) {
		t.Fatal("n should not cover n + 1")
	}
	if sum, ok := AddReserveExpr(name(), name(), lexer.Pos{}).(*ast.BinaryExpr); !ok || sum.Op != lexer.TOKEN_STAR {
		t.Fatal("known equal terms were not combined")
	}
}
