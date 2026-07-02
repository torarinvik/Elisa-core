package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func TestParseLambdaExprPreservesKeywordAndBlockBody(t *testing.T) {
	file, errs := parseSourceFile(t, "def build() -> func(i64) -> i64:\n    return fn (value: i64) -> i64:\n        return value + 1\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	lambda, ok := ret.Value.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected lambda expr, got %T", ret.Value)
	}
	if lambda.Keyword != "fn" {
		t.Fatalf("expected fn keyword to be preserved, got %q", lambda.Keyword)
	}
	if lambda.BodyExpr != nil || len(lambda.Body) != 1 {
		t.Fatalf("expected block-bodied lambda, got expr=%T body=%d", lambda.BodyExpr, len(lambda.Body))
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "return fn (value: i64) -> i64:") || !strings.Contains(formatted, "return (value + 1)") {
		t.Fatalf("expected formatted output to preserve block lambda, got:\n%s", formatted)
	}
}

func TestParseLambdaExprRemainsContextualAndPreservesLambdaRune(t *testing.T) {
	src := "def keep(lambda: int) -> int:\n    return lambda\n\ndef build() -> func(i64) -> i64:\n    return λ value: value + 1\n"
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	keep := file.Decls[0].(*ast.FuncDecl)
	ret, ok := keep.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", keep.Body[0])
	}
	if ident, ok := ret.Value.(*ast.Ident); !ok || ident.Name != "lambda" {
		t.Fatalf("expected lambda to remain an identifier, got %#v", ret.Value)
	}
	build := file.Decls[1].(*ast.FuncDecl)
	ret, ok = build.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", build.Body[0])
	}
	lambda, ok := ret.Value.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected lambda expr, got %T", ret.Value)
	}
	if lambda.Keyword != "λ" {
		t.Fatalf("expected lambda rune to be preserved, got %q", lambda.Keyword)
	}
	if !lambda.UsesShorthandParams || len(lambda.Params) != 1 || lambda.Params[0].Name != "value" {
		t.Fatalf("expected shorthand lambda params, got %#v", lambda.Params)
	}
	if formatted := unparse.FormatDecl(build); !strings.Contains(formatted, "return λ value: (value + 1)") {
		t.Fatalf("expected formatted output to preserve lambda rune, got:\n%s", formatted)
	}
}

func TestParseLambdaExprAcceptsInlineFatArrowBody(t *testing.T) {
	file, errs := parseSourceFile(t, "def build() -> func(i64) -> i64:\n    return fn (value: i64) => value + 1\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	lambda, ok := ret.Value.(*ast.LambdaExpr)
	if !ok {
		t.Fatalf("expected lambda expr, got %T", ret.Value)
	}
	if lambda.BodyExpr == nil || len(lambda.Body) != 0 {
		t.Fatalf("expected expression-bodied lambda, got expr=%T body=%d", lambda.BodyExpr, len(lambda.Body))
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "return fn (value: i64): (value + 1)") {
		t.Fatalf("expected formatted output to round-trip lambda body, got:\n%s", formatted)
	}
}
