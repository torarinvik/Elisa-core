package parser

import (
	"testing"

	"llcontext/src/ast"
)

func TestParseTypeAliasDecl(t *testing.T) {
	file, errs := parseSourceFile(t, `type NameId = u32
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(file.Decls))
	}
	decl, ok := file.Decls[0].(*ast.TypeAliasDecl)
	if !ok {
		t.Fatalf("expected type alias decl, got %T", file.Decls[0])
	}
	if decl.Name != "NameId" {
		t.Fatalf("expected alias name NameId, got %q", decl.Name)
	}
	named, ok := decl.Target.(*ast.NamedType)
	if !ok {
		t.Fatalf("expected named target type, got %T", decl.Target)
	}
	if named.Name != "u32" {
		t.Fatalf("expected target type u32, got %q", named.Name)
	}
}
