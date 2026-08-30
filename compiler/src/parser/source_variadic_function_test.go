package parser

import (
	"testing"

	"elisacore/src/ast"
)

func TestParseSourceVariadicFunction(t *testing.T) {
	src := `def format_message(format: cstr, ...) -> void:
    pass
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function declaration, got %T", file.Decls[0])
	}
	if !fn.Variadic {
		t.Fatal("expected source function to retain its variadic ABI marker")
	}
	if len(fn.Params) != 1 || fn.Params[0].Name != "format" {
		t.Fatalf("unexpected fixed parameter list: %#v", fn.Params)
	}
}
