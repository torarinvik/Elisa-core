package parser

import (
	"testing"

	"elisacore/src/ast"
)

// Phase 5b: `[errorset R]` parses as a GenericParamErrorSet generic param.
func TestParseErrorSetGenericParam(t *testing.T) {
	file, errs := parseSourceFile(t, `
def applies[T, errorset R](f: fn() -> T error[R]) -> T error[R]:
    return try f()
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected a FuncDecl, got %T", file.Decls[0])
	}
	var sawType, sawErrorSet bool
	for _, p := range fn.GenericParams {
		switch p.Kind {
		case ast.GenericParamType:
			if p.Name == "T" {
				sawType = true
			}
		case ast.GenericParamErrorSet:
			if p.Name == "R" {
				sawErrorSet = true
			}
		}
	}
	if !sawType || !sawErrorSet {
		t.Fatalf("expected type param T and errorset param R, got %#v", fn.GenericParams)
	}
}
