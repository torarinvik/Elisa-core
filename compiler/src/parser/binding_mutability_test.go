package parser

import (
	"testing"

	"elisacore/src/ast"
)

func TestExplicitLocalBindingMutabilityIsSeparateFromReferenceType(t *testing.T) {
	file, errs := parseSourceFile(t, `
def f(readonly: i64&?, writable: mutable i64&?) -> void:
	mutable rebound: i64&? = readonly
	const fixed: mutable i64&? = writable
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function declaration, got %T", file.Decls[0])
	}
	if len(fn.Body) != 2 {
		t.Fatalf("expected two local declarations, got %d", len(fn.Body))
	}
	rebound, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok || !rebound.BindingExplicit || !rebound.Mutable {
		t.Fatalf("mutable local binding should be explicit and rebindable, got %#v", fn.Body[0])
	}
	fixed, ok := fn.Body[1].(*ast.VarDeclStmt)
	if !ok || !fixed.BindingExplicit || fixed.Mutable {
		t.Fatalf("const local binding should be explicit and non-rebindable, got %#v", fn.Body[1])
	}
}
