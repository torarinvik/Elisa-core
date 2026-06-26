package parser

import (
	"testing"

	"elisacore/src/ast"
)

// A bare named refinement alias parses into a RefineDecl with no params and a where predicate.
func TestParseRefineDeclBare(t *testing.T) {
	file, errs := parseSourceFile(t, `
refine Positive = i64 where self > 0
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	rd, ok := file.Decls[0].(*ast.RefineDecl)
	if !ok {
		t.Fatalf("expected *ast.RefineDecl, got %T", file.Decls[0])
	}
	if rd.Name != "Positive" {
		t.Fatalf("expected name Positive, got %q", rd.Name)
	}
	if len(rd.TypeParams) != 0 || len(rd.Params) != 0 {
		t.Fatalf("expected no params, got typeParams=%v params=%v", rd.TypeParams, rd.Params)
	}
	if rd.Base == nil {
		t.Fatalf("expected a base type")
	}
	if _, ok := rd.Base.(*ast.NamedType); !ok {
		t.Fatalf("expected NamedType base, got %T", rd.Base)
	}
	if rd.Predicate == nil {
		t.Fatalf("expected a predicate")
	}
}

// A parametric named refinement alias parses its type-params and value-params.
func TestParseRefineDeclParametric(t *testing.T) {
	file, errs := parseSourceFile(t, `
refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	rd, ok := file.Decls[0].(*ast.RefineDecl)
	if !ok {
		t.Fatalf("expected *ast.RefineDecl, got %T", file.Decls[0])
	}
	if rd.Name != "IndexOf" {
		t.Fatalf("expected name IndexOf, got %q", rd.Name)
	}
	if len(rd.TypeParams) != 1 || rd.TypeParams[0] != "T" {
		t.Fatalf("expected one type param T, got %v", rd.TypeParams)
	}
	if len(rd.Params) != 1 || rd.Params[0].Name != "xs" {
		t.Fatalf("expected one value param xs, got %v", rd.Params)
	}
	if rd.Predicate == nil {
		t.Fatalf("expected a predicate")
	}
}

// `refine` is a contextual keyword: an identifier merely spelled `refine` is not a decl.
func TestRefineIsContextualKeyword(t *testing.T) {
	file, errs := parseSourceFile(t, `
def f() -> i64:
    refine = 3
    return refine
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors when `refine` is used as an identifier: %v", errs)
	}
	if _, ok := file.Decls[0].(*ast.FuncDecl); !ok {
		t.Fatalf("expected the function decl to parse normally, got %T", file.Decls[0])
	}
}
