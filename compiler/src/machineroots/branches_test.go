package machineroots

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestMarkerInElif(t *testing.T) {
	pos := lexer.Pos{Line: 12}
	branch := &ast.IfStmt{Elifs: []ast.ElifClause{{Body: []ast.Stmt{&ast.BreakStmt{Position: pos}}}}}
	if !HasBranchMarker(branch, map[lexer.Pos]string{pos: "next"}) {
		t.Fatal("missed transition in elif")
	}
	if HasBranchMarker(branch, nil) {
		t.Fatal("found an unmarked transition")
	}
}

func TestNestedFallthroughLocalInElif(t *testing.T) {
	nested := &ast.IfStmt{Then: []ast.Stmt{&ast.VarDeclStmt{}}}
	branch := &ast.IfStmt{Elifs: []ast.ElifClause{{Body: []ast.Stmt{nested, &ast.ReturnStmt{}}}}}
	if !HasFallthroughLocalDecl(branch, nil) {
		t.Fatal("missed nested fallthrough local in terminating elif")
	}
	nested.Then = append(nested.Then, &ast.ReturnStmt{})
	if HasFallthroughLocalDecl(branch, nil) {
		t.Fatal("terminating nested branch cannot leak a local")
	}
}
