package parser

import (
	"testing"

	"elisacore/src/ast"
)

// Regression: a statement-position block-form `catch` followed by another
// statement must parse (previously the bare-expr-stmt path called expectNewline
// directly, which fails after the catch's terminating DEDENT).
func TestParseCatchStatementFollowedByStatement(t *testing.T) {
	file, errs := parseSourceFile(t, `
error E:
    Bad

extern f() -> void error[E]

def run() -> i64:
    catch f():
        n:
            true
        error e:
            return 1
    return 2
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[2].(*ast.FuncDecl)
	if len(fn.Body) != 2 {
		t.Fatalf("expected 2 statements (catch, return), got %d", len(fn.Body))
	}
	if _, ok := fn.Body[0].(*ast.ExprStmt); !ok {
		t.Fatalf("expected first statement to be the catch ExprStmt, got %T", fn.Body[0])
	}
	if _, ok := fn.Body[1].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected trailing return to parse as its own statement, got %T", fn.Body[1])
	}
}
