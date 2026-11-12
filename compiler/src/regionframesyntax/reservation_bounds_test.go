package regionframesyntax

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestReservationBounds(t *testing.T) {
	bound := &ast.Ident{Name: "n"}
	loop := &ast.ForStmt{Start: &ast.IntLit{Value: "0"}, End: bound, Op: lexer.TOKEN_RANGE_LT}
	if got, ok := CountingLoopBound(loop); !ok || got != bound {
		t.Fatal("missed counting bound")
	}
	loop.Reverse = true
	if _, ok := CountingLoopBound(loop); ok {
		t.Fatal("accepted reverse loop")
	}
	expr := &ast.BinaryExpr{Left: bound, Op: lexer.TOKEN_PLUS, Right: &ast.IntLit{Value: "1"}}
	if !BoundInScopeAtRegionEntry(expr, map[string]bool{"n": true}) || BoundInScopeAtRegionEntry(expr, nil) {
		t.Fatal("incorrect bound scope")
	}
	if got := HardBoundPushElementCount(&ast.CallExpr{Args: []ast.Expr{&ast.ListLitExpr{Elems: []ast.Expr{bound, bound}}}}); got != 2 {
		t.Fatalf("push count %d", got)
	}
}

func TestStackBlockBoundEvaluation(t *testing.T) {
	if got, ok := ParseStackBlockIntLit("1_024"); !ok || got != 1024 {
		t.Fatalf("literal: %d %v", got, ok)
	}
	param := &ast.Ident{Name: "n"}
	cast := &ast.CastExpr{Target: &ast.NamedType{Name: "u32"}, Operand: param}
	if !StackBlockBoundEvaluable(cast, map[string]bool{"n": true}) {
		t.Fatal("rejected integer cast of parameter")
	}
	if StackBlockBoundEvaluable(cast, nil) {
		t.Fatal("accepted unavailable parameter")
	}
	cast.Target = &ast.NamedType{Name: "f64"}
	if StackBlockBoundEvaluable(cast, map[string]bool{"n": true}) {
		t.Fatal("accepted floating cast")
	}
}
