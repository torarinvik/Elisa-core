package parser

import (
	"testing"

	"elisacore/src/ast"
)

// `match <expr> as <ok>:` desugars to a CatchExpr (ExprStmt-wrapped): the `ok:` arm
// is the success body, error arms become catch arms (with payload binders), and
// `else:` becomes an error-binding catch-all.
func TestParseMatchAsDesugarsToCatch(t *testing.T) {
	src := `error E:
    Bad1
    Bad2(value: u32)

def g() -> void:
    match f() as n:
        ok: use(n)
        E.Bad1: a()
        E.Bad2(x): b(x)
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[1].(*ast.FuncDecl)
	exprStmt, ok := fn.Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected ExprStmt, got %T", fn.Body[0])
	}
	catch, ok := exprStmt.Expr.(*ast.CatchExpr)
	if !ok {
		t.Fatalf("expected CatchExpr from match-as, got %T", exprStmt.Expr)
	}
	if catch.Success.Name != "n" {
		t.Fatalf("expected success binding 'n', got %q", catch.Success.Name)
	}
	if len(catch.Arms) != 2 {
		t.Fatalf("expected 2 error arms, got %d", len(catch.Arms))
	}
	if len(catch.Arms[1].Payload) != 1 || catch.Arms[1].Payload[0] != "x" {
		t.Fatalf("expected payload binder x on second arm, got %v", catch.Arms[1].Payload)
	}
}
