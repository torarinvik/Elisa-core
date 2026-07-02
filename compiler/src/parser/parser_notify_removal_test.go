package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// The `notify one` / `notify all` condition-variable notification statement has been
// removed: its raw `notify_one` / `notify_all` desugaring is already rejected for user
// code as removed raw concurrency surface, so the sugar only ever produced a two-stage
// failure. It now fails immediately at parse time with a directed diagnostic.
func TestParseNotifyOneStmtRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "def wake(cv: CondVar) -> void:\n    notify one cv\n")
	if !strings.Contains(strings.Join(errs, "\n"), "the `notify one` / `notify all` statement has been removed") {
		t.Fatalf("expected notify removal diagnostic, got: %v", errs)
	}
}

func TestParseNotifyAllStmtRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "def wake(cv: CondVar) -> void:\n    notify all cv\n")
	if !strings.Contains(strings.Join(errs, "\n"), "the `notify one` / `notify all` statement has been removed") {
		t.Fatalf("expected notify removal diagnostic, got: %v", errs)
	}
}

// `notify` is not reserved: it remains usable as an ordinary identifier (a bare `notify`
// with no `one`/`all` follower is an ordinary expression, and `notify` binds as a local).
func TestParseNotifyRemainsContextualIdentifier(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(notify: int) -> int:\n    return notify\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	if ident, ok := ret.Value.(*ast.Ident); !ok || ident.Name != "notify" {
		t.Fatalf("expected notify to remain an identifier, got %#v", ret.Value)
	}
}

// Structured `wait all` (task-group join) is a separate, retained construct — the notify
// removal must not touch it.
func TestParseWaitAllStmtStillAccepted(t *testing.T) {
	_, errs := parseSourceFile(t, "def joinall(g: TaskGroup) -> void:\n    wait all g\n")
	if len(errs) != 0 {
		t.Fatalf("wait all should still parse, got: %v", errs)
	}
}
