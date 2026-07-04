package parser

import (
	"testing"

	"elisacore/src/ast"
)

// A statement-level `if` directly after a multi-line match-expression must parse as a
// STATEMENT, not as a postfix ternary of the last arm's value. The match arms end in a
// DEDENT that sits immediately before the `if` token, which previously made the ternary
// layer swallow it (`v if s == "" else …` → "expected else" cascade).
func TestIfStatementAfterMatchExprIsNotTernary(t *testing.T) {
	src := "def f(k: int, v: sview) -> sview:\n" +
		"    s: sview = match k:\n" +
		"        1: \"one\"\n" +
		"        _: v\n" +
		"    if s == \"\":\n" +
		"        return v\n" +
		"    return s\n"
	file, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected FuncDecl, got %T", file.Decls[0])
	}
	// Body: var decl, if statement, return — the `if` must survive as its own statement.
	if len(fn.Body) != 3 {
		t.Fatalf("expected 3 body statements (decl, if, return), got %d", len(fn.Body))
	}
	if _, ok := fn.Body[1].(*ast.IfStmt); !ok {
		t.Fatalf("statement after match-expr decl must be an IfStmt, got %T", fn.Body[1])
	}
}

// The guard must NOT break a genuine inline postfix ternary.
func TestInlinePostfixTernaryStillParses(t *testing.T) {
	src := "def f(c: bool, a: i64, b: i64) -> i64:\n" +
		"    r: i64 = a if c else b\n" +
		"    return r\n"
	_, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
}
