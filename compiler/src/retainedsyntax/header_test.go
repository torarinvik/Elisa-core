package retainedsyntax

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"testing"
)

func TestHeaderTracksContractsButIgnoresBodyAndPositions(t *testing.T) {
	original := &ast.FuncDecl{Name: "check", Requires: []ast.Expr{&ast.Ident{Name: "ready", Position: lexer.Pos{Line: 1}}}}
	baseline := Header(original)
	edited := *original
	edited.Body = []ast.Stmt{&ast.ReturnStmt{Value: &ast.IntLit{Value: "1"}}}
	edited.Requires = []ast.Expr{&ast.Ident{Name: "ready", Position: lexer.Pos{Line: 37}}}
	if Header(&edited) != baseline {
		t.Fatal("body and position changes must not alter the header")
	}
	edited.Requires = []ast.Expr{&ast.Ident{Name: "other"}}
	if Header(&edited) == baseline {
		t.Fatal("contract edits must change the header")
	}
	edited = *original
	edited.DecreasesWild = "reason"
	if Header(&edited) == baseline {
		t.Fatal("termination opt-out must change the header")
	}
}
