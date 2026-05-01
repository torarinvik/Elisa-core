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

func TestParseIDTypeAliasSpellings(t *testing.T) {
	file, errs := parseSourceFile(t, `type NameId = id[Name]
type SymbolId = Id[Symbol]
type ScopeId = ID[Scope]
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 3 {
		t.Fatalf("expected 3 decls, got %d", len(file.Decls))
	}
	for i, declNode := range file.Decls {
		decl, ok := declNode.(*ast.TypeAliasDecl)
		if !ok {
			t.Fatalf("expected type alias decl, got %T", declNode)
		}
		builtin, ok := decl.Target.(*ast.BuiltinTypeExpr)
		if !ok {
			t.Fatalf("decl %d: expected id builtin type expr, got %T", i, decl.Target)
		}
		if builtin.Name != "id" {
			t.Fatalf("decl %d: expected canonical id name, got %q", i, builtin.Name)
		}
		if len(builtin.TypeArgs) != 1 {
			t.Fatalf("decl %d: expected one tag type arg, got %d", i, len(builtin.TypeArgs))
		}
	}
}
