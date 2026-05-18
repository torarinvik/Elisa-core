package parser

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

func TestParseIndexFallbackExpr(t *testing.T) {
	file, errs := parseSourceFile(t, `
def read(xs: darray[int], i: usize) -> int:
    return xs[i] else 0
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	funcDecl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	ret, ok := funcDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", funcDecl.Body[0])
	}
	indexExpr, ok := ret.Value.(*ast.IndexExpr)
	if !ok {
		t.Fatalf("expected index expr, got %T", ret.Value)
	}
	if indexExpr.Fallback == nil {
		t.Fatal("expected index fallback expression")
	}
	if got := unparse.FormatExpr(indexExpr); got != "xs[i] else 0" {
		t.Fatalf("expected canonical index fallback, got %q", got)
	}
}

func TestParseUnaryMinusBindsInsideTernaryValue(t *testing.T) {
	file, errs := parseSourceFile(t, `
def compare(a: u8, b: u8) -> int:
    return -1 if a < b else 1
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	funcDecl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	ret, ok := funcDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", funcDecl.Body[0])
	}
	ternary, ok := ret.Value.(*ast.TernaryExpr)
	if !ok {
		t.Fatalf("expected ternary expr, got %T", ret.Value)
	}
	value, ok := ternary.Value.(*ast.UnaryExpr)
	if !ok || value.Op != lexer.TOKEN_MINUS {
		t.Fatalf("expected ternary value to be unary minus, got %#v", ternary.Value)
	}
	if _, nested := value.Operand.(*ast.TernaryExpr); nested {
		t.Fatal("unary minus must not wrap the whole ternary")
	}
	if got := unparse.FormatExpr(ternary); got != "((-1) if (a < b) else 1)" {
		t.Fatalf("expected canonical ternary, got %q", got)
	}
}
