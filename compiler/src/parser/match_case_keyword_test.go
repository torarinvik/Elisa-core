package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// Binary expressions starting with an identifier (`return v * v`) parse normally in a
// match-arm body. This pins the regression where a stray `case` keyword sent the arm
// into recovery and surfaced as bogus "postfix return guard expects `if`" /
// "expected newline" errors on the body.
func TestParseMatchArmBodyIdentMultiplication(t *testing.T) {
	src := `enum E:
    A(v: i64)

def g(e: E) -> i64:
    match e:
        E.A(v: v):
            return v * v
    return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[1].(*ast.FuncDecl)
	matchStmt, ok := fn.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected MatchStmt, got %T", fn.Body[0])
	}
	ret, ok := matchStmt.Arms[0].Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected ReturnStmt in arm body, got %T", matchStmt.Arms[0].Body[0])
	}
	bin, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected BinaryExpr return value, got %T", ret.Value)
	}
	if _, ok := bin.Left.(*ast.Ident); !ok {
		t.Fatalf("expected Ident lhs, got %T", bin.Left)
	}
}

// A leading `case` in a match arm gets one targeted diagnostic and is skipped, so the
// pattern and body still parse (no cascade errors into the arm body).
func TestParseMatchArmCaseKeywordDiagnosed(t *testing.T) {
	src := `enum E:
    A(v: i64)

def g(e: E) -> i64:
    match e:
        case E.A(v: v):
            return v * v
    return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 parser error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0], "match arms do not use a `case` keyword") {
		t.Fatalf("expected case-keyword diagnostic, got: %s", errs[0])
	}
	fn := file.Decls[1].(*ast.FuncDecl)
	matchStmt := fn.Body[0].(*ast.MatchStmt)
	variant, ok := matchStmt.Arms[0].Pattern.(*ast.MatchVariantPattern)
	if !ok {
		t.Fatalf("expected MatchVariantPattern after recovery, got %T", matchStmt.Arms[0].Pattern)
	}
	if variant.EnumName != "E" || variant.Variant != "A" {
		t.Fatalf("expected E.A pattern, got %s.%s", variant.EnumName, variant.Variant)
	}
	if _, ok := matchStmt.Arms[0].Body[0].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected ReturnStmt arm body after recovery, got %T", matchStmt.Arms[0].Body[0])
	}
}

// A bind arm literally named `case` (i.e. `case:`) is still a plain binding, not the
// keyword diagnostic.
func TestParseMatchArmBindNamedCaseStillAllowed(t *testing.T) {
	src := `def g(x: i64) -> i64:
    match x:
        case:
            return case
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	matchStmt := fn.Body[0].(*ast.MatchStmt)
	bind, ok := matchStmt.Arms[0].Pattern.(*ast.MatchBindPattern)
	if !ok || bind.Name != "case" {
		t.Fatalf("expected bind pattern named case, got %T", matchStmt.Arms[0].Pattern)
	}
}
