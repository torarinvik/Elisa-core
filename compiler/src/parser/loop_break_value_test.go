package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// docs/119 §3 `break value` and its docs/125 §6b postfix guard. `break v` in a loop whose
// header yields a simple accumulator desugars to `if true: acc <- v; break`; the guarded
// spelling `break v if cond` wraps that whole statement in `if cond:`, exactly like every
// other postfix-guarded statement (see takeStmtGuard).

// The body of the first range loop found in a `name: T = for ...` value in the first function.
func loopHeaderBody(t *testing.T, file *ast.File) []ast.Stmt {
	t.Helper()
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("first declaration is %T, want *ast.FuncDecl", file.Decls[0])
	}
	for _, stmt := range fn.Body {
		decl, ok := stmt.(*ast.VarDeclStmt)
		if !ok {
			continue
		}
		block, ok := decl.Value.(*ast.ExprBlock)
		if !ok {
			continue
		}
		for _, inner := range block.Stmts {
			if loop, ok := inner.(*ast.ForStmt); ok {
				return loop.Body
			}
		}
	}
	t.Fatalf("no loop-header range loop in the first function")
	return nil
}

// The `if true: acc <- v; break` desugaring, returning the assigned value.
func valueBreakAssignment(t *testing.T, stmt ast.Stmt, accumulator string) ast.Expr {
	t.Helper()
	desugared, ok := stmt.(*ast.IfStmt)
	if !ok {
		t.Fatalf("value break is %T, want *ast.IfStmt", stmt)
	}
	if cond, ok := desugared.Cond.(*ast.BoolLit); !ok || !cond.Value {
		t.Fatalf("value break condition is %#v, want the literal true", desugared.Cond)
	}
	if len(desugared.Then) != 2 || len(desugared.Else) != 0 {
		t.Fatalf("value break body has %d/%d statements, want assign+break", len(desugared.Then), len(desugared.Else))
	}
	assign, ok := desugared.Then[0].(*ast.AssignStmt)
	if !ok {
		t.Fatalf("value break first statement is %T, want *ast.AssignStmt", desugared.Then[0])
	}
	if target, ok := assign.Target.(*ast.Ident); !ok || target.Name != accumulator {
		t.Fatalf("value break assigns %#v, want accumulator %q", assign.Target, accumulator)
	}
	if _, ok := desugared.Then[1].(*ast.BreakStmt); !ok {
		t.Fatalf("value break second statement is %T, want *ast.BreakStmt", desugared.Then[1])
	}
	return assign.Value
}

func TestBreakValueDesugarsToAccumulatorWrite(t *testing.T) {
	src := "def f(n: i64) -> bool:\n    found: bool = for i in 0..<n |hit = false| -> hit:\n        break true\n    return found\n"
	file, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	body := loopHeaderBody(t, file)
	if len(body) != 1 {
		t.Fatalf("loop body has %d statements, want 1", len(body))
	}
	if value, ok := valueBreakAssignment(t, body[0], "hit").(*ast.BoolLit); !ok || !value.Value {
		t.Fatalf("break value is %#v, want the literal true", value)
	}
}

func TestBreakValuePostfixGuardWrapsTheWholeBreak(t *testing.T) {
	src := "def f(n: i64) -> i64:\n    at: i64 = for i in 0..<n |pos = -1| -> pos:\n        break i if i * i > n\n    return at\n"
	file, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	body := loopHeaderBody(t, file)
	if len(body) != 1 {
		t.Fatalf("loop body has %d statements, want 1", len(body))
	}
	guard, ok := body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("guarded value break is %T, want *ast.IfStmt", body[0])
	}
	if _, ok := guard.Cond.(*ast.BinaryExpr); !ok {
		t.Fatalf("guard condition is %T, want the written comparison", guard.Cond)
	}
	if len(guard.Then) != 1 || len(guard.Else) != 0 {
		t.Fatalf("guard body has %d/%d statements, want exactly the value break", len(guard.Then), len(guard.Else))
	}
	if guard.FromSource {
		t.Fatalf("a postfix-guard desugaring must not carry the written block-`if` mark")
	}
	if value, ok := valueBreakAssignment(t, guard.Then[0], "pos").(*ast.Ident); !ok || value.Name != "i" {
		t.Fatalf("guarded break value is %#v, want the identifier i", value)
	}
}

// A guard is recorded only at a statement terminator and consumed by the break: it must not
// leak onto the next statement.
func TestBreakValueGuardDoesNotLeakToTheNextStatement(t *testing.T) {
	src := "def f(n: i64) -> i64:\n    at: i64 = for i in 0..<n |pos = -1| -> pos:\n        break i if i > 3\n        pos <- i\n    return at\n"
	file, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	body := loopHeaderBody(t, file)
	if len(body) != 2 {
		t.Fatalf("loop body has %d statements, want 2", len(body))
	}
	if _, ok := body[1].(*ast.AssignStmt); !ok {
		t.Fatalf("statement after the guarded break is %T, want a plain *ast.AssignStmt", body[1])
	}
}

// A COMPLETED ternary is a value, and may itself take a guard; an else-less `if` inside
// brackets stays a (malformed) ternary rather than a guard.
func TestBreakValueTernaryAndGuardCompose(t *testing.T) {
	src := "def f(n: i64) -> i64:\n    at: i64 = for i in 0..<n |pos = -1| -> pos:\n        break (1 if i > 2 else 2) if i > 1\n    return at\n"
	file, errs, _ := parseSourceWithNotices(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	guard, ok := loopHeaderBody(t, file)[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("guarded ternary break is %T, want *ast.IfStmt", loopHeaderBody(t, file)[0])
	}
	valueBreakAssignment(t, guard.Then[0], "pos")

	_, errs, _ = parseSourceWithNotices(t, "def f(n: i64) -> i64:\n    at: i64 = for i in 0..<n |pos = -1| -> pos:\n        break (1 if i > 2)\n    return at\n")
	if len(errs) == 0 {
		t.Fatalf("a bracketed else-less `if` must stay a ternary that demands its `else`")
	}
}

func TestBreakValueGuardOutsideAValueLoopIsRejected(t *testing.T) {
	src := "def f(n: i64) -> i64:\n    for i in 0..<n:\n        break i if i > 3\n    return n\n"
	_, errs, _ := parseSourceWithNotices(t, src)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "`break value` needs a loop with a simple `-> accumulator` yield") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the break-value target diagnostic, got: %v", errs)
	}
}
