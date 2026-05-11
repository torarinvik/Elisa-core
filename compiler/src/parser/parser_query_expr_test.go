package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
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

func TestParseExpressionWhereViewWithExplicitBinder(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(items: darray[i64]) -> bool:\n    return all((items where item: item > 0))\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected all call, got %T", ret.Value)
	}
	paren, ok := call.Args[0].(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected parenthesized where view arg, got %T", call.Args[0])
	}
	whereCall, ok := paren.Inner.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected where call, got %T", paren.Inner)
	}
	callee, ok := whereCall.Func.(*ast.Ident)
	if !ok || callee.Name != "where" {
		t.Fatalf("expected where callee, got %T %#v", whereCall.Func, whereCall.Func)
	}
	if len(whereCall.Args) != 2 {
		t.Fatalf("expected where source and predicate args, got %d", len(whereCall.Args))
	}
	lambda, ok := whereCall.Args[1].(*ast.LambdaExpr)
	if !ok || len(lambda.Params) != 1 || lambda.Params[0].Name != "item" {
		t.Fatalf("expected shorthand item lambda, got %T %#v", whereCall.Args[1], whereCall.Args[1])
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "return all((items where item: (item > 0)))") {
		t.Fatalf("expected formatted expression where view, got:\n%s", formatted)
	}
}

func TestParseExpressionWhereViewWithEnumerateBinders(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(items: darray[i64]) -> bool:\n    return all((items.enumerate() where index, value: index > 0 and value > 2))\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	paren := call.Args[0].(*ast.ParenExpr)
	whereCall := paren.Inner.(*ast.CallExpr)
	lambda, ok := whereCall.Args[1].(*ast.LambdaExpr)
	if !ok || len(lambda.Params) != 1 || lambda.Params[0].Name != "__where_item" {
		t.Fatalf("expected rewritten enumerate tuple lambda, got %T %#v", whereCall.Args[1], whereCall.Args[1])
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "items.enumerate() where index, value: ((index > 0) and (value > 2))") {
		t.Fatalf("expected formatted enumerate where predicate, got:\n%s", formatted)
	}
}
