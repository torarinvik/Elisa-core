package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// The `cascade <target>:` statement form has been removed.
func TestParseCascadeStmtRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "struct Inner:\n    value: int\n\nstruct Report:\n    inner: Inner\n\ndef build(report: Report, value: int) -> void:\n    cascade report:\n        .inner.value <- value\n")
	if !strings.Contains(strings.Join(errs, "\n"), "the `cascade` statement has been removed") {
		t.Fatalf("expected cascade statement removal diagnostic, got: %v", errs)
	}
}

// The `cascade <target> => <value>` expression form has been removed.
func TestParseCascadeExprRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "struct Row:\n    ref_count: int\n\ndef nonzero(row: Row) -> bool:\n    return cascade row => .ref_count != 0\n")
	if !strings.Contains(strings.Join(errs, "\n"), "the `cascade` expression has been removed") {
		t.Fatalf("expected cascade expression removal diagnostic, got: %v", errs)
	}
}

// `cascade` is not a reserved word: it remains usable as an ordinary identifier.
func TestParseCascadeRemainsContextualIdentifier(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(cascade: int) -> int:\n    return cascade\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	if ident, ok := ret.Value.(*ast.Ident); !ok || ident.Name != "cascade" {
		t.Fatalf("expected cascade to remain an identifier, got %#v", ret.Value)
	}
}
