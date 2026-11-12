package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestStructFreshWalkChecksConstructedValuesAndWrites(t *testing.T) {
	lit := &ast.StructLitExpr{}
	target := &ast.Ident{Name: "out"}
	body := []ast.Stmt{&ast.IfStmt{Then: []ast.Stmt{&ast.VarDeclStmt{Name: "out", Value: lit}, &ast.AssignStmt{Target: target}}}}
	unwrap := func(e ast.Expr) ast.Expr { return e }
	literals, writes := 0, 0
	ok := StructFieldsRemainFresh(body, map[string]bool{"out": true}, unwrap, func(*ast.StructLitExpr) bool { literals++; return true }, func(ast.Expr) bool { writes++; return false })
	if ok || literals != 1 || writes != 1 {
		t.Fatalf("ok=%v literals=%d writes=%d", ok, literals, writes)
	}
}
