package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func TestParseLambdaExprPreservesKeywordAndBlockBody(t *testing.T) {
	file, errs := parseSourceFile(t, "def build() -> fn(i64) -> i64:\n    return fn (value: i64) -> i64:\n        return value + 1\n")
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
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "return fn(value: i64) -> i64:") || !strings.Contains(formatted, "return (value + 1)") {
		t.Fatalf("expected formatted output to preserve block lambda, got:\n%s", formatted)
	}
}

func TestParseLambdaExprRemainsContextualAndPreservesLambdaRune(t *testing.T) {
	src := "def keep(lambda: int) -> int:\n    return lambda\n\ndef build() -> fn(i64) -> i64:\n    return λ(value) => value + 1\n"
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
	if lambda.UsesShorthandParams || len(lambda.Params) != 1 || lambda.Params[0].Name != "value" {
		t.Fatalf("expected parenthesized lambda params, got %#v", lambda.Params)
	}
	if formatted := unparse.FormatDecl(build); !strings.Contains(formatted, "return λ(value) => (value + 1)") {
		t.Fatalf("expected formatted output to preserve lambda rune, got:\n%s", formatted)
	}
}

func TestParseLambdaExprAcceptsInlineFatArrowBody(t *testing.T) {
	file, errs := parseSourceFile(t, "def build() -> fn(i64) -> i64:\n    return fn (value: i64) => value + 1\n")
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
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "return fn(value: i64) => (value + 1)") {
		t.Fatalf("expected formatted output to round-trip lambda body, got:\n%s", formatted)
	}
}

// The `lambda` keyword has been removed in favor of `fn`, which is contextual (a bare
// `fn` / `fn(x)` call stays an ordinary reference), plus any Unicode lambda letter.
func TestParseLambdaKeywordFnAndUnicodeFamily(t *testing.T) {
	// Every accepted lambda head (canonical `fn` + the Unicode lambda letters) parses.
	// The BMP lambda letters (Greek λ/Λ and Latin lambda-with-stroke ƛ) plus `fn`.
	// The supplementary-plane math-alphanumeric variants are intentionally excluded.
	heads := []string{"fn", "λ", "Λ", "ƛ"}
	for _, h := range heads {
		src := "def use() -> void:\n    g: T = " + h + "(x) => x\n"
		if _, errs := parseSourceFile(t, src); len(errs) != 0 {
			t.Fatalf("lambda head %q should parse, got: %v", h, errs)
		}
	}
}

func TestParseLambdaKeywordRejectsRemovedLambdaSpelling(t *testing.T) {
	_, errs := parseSourceFile(t, "def use() -> void:\n    g: T = lambda x: x\n")
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "the `lambda` keyword has been removed; use `fn`") {
		t.Fatalf("expected the lambda removal diagnostic, got: %v", errs)
	}
}

// `fn` stays an ordinary identifier: a call and a bare reference are not lambdas.
func TestParseFnRemainsOrdinaryIdentifier(t *testing.T) {
	src := "def apply(fn: fn(int) -> int, x: int) -> int:\n    return fn(x)\n"
	if _, errs := parseSourceFile(t, src); len(errs) != 0 {
		t.Fatalf("`fn` as a param + call should parse, got: %v", errs)
	}
}

// The `func` type keyword has been renamed to `fn` (one keyword for "function" in both
// type and value position). `func` in type position and `export func` are directed errors.
func TestParseFuncTypeKeywordRenamedToFn(t *testing.T) {
	// `fn(...) -> T` in type position parses; a param named `fn` with an `fn` type is fine.
	if _, errs := parseSourceFile(t, "def apply(fn: fn(i64) -> i64, x: i64) -> i64:\n    return fn(x)\n"); len(errs) != 0 {
		t.Fatalf("`fn` function type should parse, got: %v", errs)
	}
	// `func` in type position is a directed error.
	_, errs := parseSourceFile(t, "def apply(f: func(i64) -> i64) -> void:\n    pass\n")
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "the `func` type keyword has been renamed to `fn`") {
		t.Fatalf("expected the func-rename diagnostic, got: %v", errs)
	}
	// A variable named `func` (value position) is untouched by the rename.
	if _, errs := parseSourceFile(t, "def g(func: i64) -> i64:\n    return func + 1\n"); len(errs) != 0 {
		t.Fatalf("`func` as a variable name should still parse, got: %v", errs)
	}
}

func TestParseExportFuncRenamedToExportFn(t *testing.T) {
	if _, errs := parseSourceFile(t, "def impl(x: i64) -> i64:\n    return x\n\nexport fn probe(x: i64) -> i64 = impl\n"); len(errs) != 0 {
		t.Fatalf("`export fn` should parse, got: %v", errs)
	}
	_, errs := parseSourceFile(t, "def impl(x: i64) -> i64:\n    return x\n\nexport func probe(x: i64) -> i64 = impl\n")
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "`export func` has been renamed to `export fn`") {
		t.Fatalf("expected the export-func rename diagnostic, got: %v", errs)
	}
}
