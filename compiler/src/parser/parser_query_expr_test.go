package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/unparse"
)

func TestParseQueryExprFamily(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(items: darray[i64]) -> bool:\n    return any item in items where item > 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok {
		t.Fatalf("expected query expr, got %T", ret.Value)
	}
	if query.Kind != ast.QueryExprAny || query.Name != "item" {
		t.Fatalf("unexpected query shape: %#v", query)
	}
	if _, ok := query.Filter.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected binary filter, got %T", query.Filter)
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "return any item in items where (item > 0)") {
		t.Fatalf("expected formatted query expression, got:\n%s", formatted)
	}
}
