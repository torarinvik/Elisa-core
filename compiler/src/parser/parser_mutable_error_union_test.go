package parser

import (
	"testing"

	"elisacore/src/ast"
)

// `mutable T& error[E]`: the qualifier belongs to the success type. It used to wrap the whole
// error union, where the analyzer dropped it (only a reference or a view can be made mutable), so
// a function declared to return a writable reference returned a read-only one.
func TestParseMutableQualifierBindsToErrorUnionValue(t *testing.T) {
	file, errs := parseSourceFile(t, `
def f(n: mutable Node&) -> mutable Node& error[MemoryError]:
    return n
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected a FuncDecl, got %T", file.Decls[0])
	}
	union, ok := fn.ReturnType.(*ast.ErrorUnionTypeExpr)
	if !ok {
		t.Fatalf("expected an ErrorUnionTypeExpr return, got %T", fn.ReturnType)
	}
	if _, ok := union.Value.(*ast.MutableType); !ok {
		t.Fatalf("expected the union's success type to be mutable, got %T", union.Value)
	}
	if _, ok := union.Errors.(*ast.ErrorSetExpr); !ok {
		t.Fatalf("expected an ErrorSetExpr, got %T", union.Errors)
	}
}

// Without an error clause `mutable T&` is unchanged.
func TestParseMutableRefTypeWithoutErrorUnion(t *testing.T) {
	file, errs := parseSourceFile(t, `
def f(n: mutable Node&) -> mutable Node&:
    return n
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected a FuncDecl, got %T", file.Decls[0])
	}
	if _, ok := fn.ReturnType.(*ast.MutableType); !ok {
		t.Fatalf("expected a MutableType return, got %T", fn.ReturnType)
	}
}
