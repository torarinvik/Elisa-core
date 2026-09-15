package parser

import (
	"testing"

	"elisacore/src/ast"
)

func TestParseValueFunctionTailAsReturn(t *testing.T) {
	file, errs := parseSourceFile(t, `def scalar() -> i64:
    41 + 1

def choose(value: i64) -> i64:
    if value < 0:
        7
    else:
        11
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	scalar := file.Decls[0].(*ast.FuncDecl)
	if _, ok := scalar.Body[0].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected scalar tail to become ReturnStmt, got %T", scalar.Body[0])
	}
	choose := file.Decls[1].(*ast.FuncDecl)
	ifStmt, ok := choose.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected choose body to remain IfStmt, got %T", choose.Body[0])
	}
	if _, ok := ifStmt.Then[0].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected then tail to become ReturnStmt, got %T", ifStmt.Then[0])
	}
	if _, ok := ifStmt.Else[0].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected else tail to become ReturnStmt, got %T", ifStmt.Else[0])
	}
}

func TestParseValueFunctionDoesNotReturnLoopTail(t *testing.T) {
	file, errs := parseSourceFile(t, `def loop_only(value: bool) -> i64:
    while value:
        1
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	if _, ok := fn.Body[0].(*ast.WhileStmt); !ok {
		t.Fatalf("expected loop tail to remain WhileStmt, got %T", fn.Body[0])
	}
}

func TestParseVoidFunctionDoesNotConvertTail(t *testing.T) {
	file, errs := parseSourceFile(t, `def note() -> void:
    1
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	if _, ok := fn.Body[0].(*ast.ExprStmt); !ok {
		t.Fatalf("expected void tail to remain ExprStmt, got %T", fn.Body[0])
	}
}

func TestParseUnannotatedFunctionDoesNotConvertTail(t *testing.T) {
	file, errs := parseSourceFile(t, `def note():
    1
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	if _, ok := fn.Body[0].(*ast.ExprStmt); !ok {
		t.Fatalf("expected unannotated tail to remain ExprStmt, got %T", fn.Body[0])
	}
}
