package parser

import (
	"testing"

	"llcontext/src/ast"
	"llcontext/src/unparse"
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
