package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
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
type SymbolRow = RowId[SymbolRows]
type GuestEntryPoint = ptrid[GuestEntryPointRole]
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 5 {
		t.Fatalf("expected 5 decls, got %d", len(file.Decls))
	}
	for i, declNode := range file.Decls[:3] {
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
	rowDecl, ok := file.Decls[3].(*ast.TypeAliasDecl)
	if !ok {
		t.Fatalf("expected RowId type alias decl, got %T", file.Decls[3])
	}
	rowBuiltin, ok := rowDecl.Target.(*ast.BuiltinTypeExpr)
	if !ok || rowBuiltin.Name != "RowId" || len(rowBuiltin.TypeArgs) != 1 {
		t.Fatalf("expected RowId builtin type expr, got %T %#v", rowDecl.Target, rowDecl.Target)
	}
}

func TestParseModuleDeclPreservesModuleSpelling(t *testing.T) {
	file, errs := parseSourceFile(t, `module Handles:
    extern Symbol
    type SymbolId = id[Symbol]
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(file.Decls))
	}
	module, ok := file.Decls[0].(*ast.NamespaceDecl)
	if !ok {
		t.Fatalf("expected module to parse as namespace decl, got %T", file.Decls[0])
	}
	if !module.Module {
		t.Fatal("expected module spelling to be preserved")
	}
	if module.Name != "Handles" {
		t.Fatalf("expected module name Handles, got %q", module.Name)
	}
	if len(module.Decls) != 2 {
		t.Fatalf("expected 2 module decls, got %d", len(module.Decls))
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "module Handles:") {
		t.Fatalf("expected formatter to preserve module spelling, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "namespace Handles:") {
		t.Fatalf("expected formatter not to rewrite module spelling to namespace, got:\n%s", formatted)
	}
}

func TestParseConstModuleDeclPreservesConstModuleSpelling(t *testing.T) {
	file, errs := parseSourceFile(t, `const module OS:
    WIN = 1
    MAC: i32 = 2
    UNIX = 3

def is_win(os: i32) -> bool:
    return os == OS::WIN
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 2 {
		t.Fatalf("expected 2 decls, got %d", len(file.Decls))
	}
	module, ok := file.Decls[0].(*ast.NamespaceDecl)
	if !ok {
		t.Fatalf("expected const module to parse as namespace decl, got %T", file.Decls[0])
	}
	if !module.Module || !module.Const {
		t.Fatalf("expected const module flags to be preserved, got Module=%v Const=%v", module.Module, module.Const)
	}
	if module.Name != "OS" {
		t.Fatalf("expected module name OS, got %q", module.Name)
	}
	if len(module.Decls) != 3 {
		t.Fatalf("expected 3 const module members, got %d", len(module.Decls))
	}
	if first, ok := module.Decls[0].(*ast.ConstDecl); !ok || first.Name != "WIN" {
		t.Fatalf("expected first const module member WIN, got %T %#v", module.Decls[0], module.Decls[0])
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "const module OS:") {
		t.Fatalf("expected formatter to preserve const module spelling, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "    WIN = 1") || !strings.Contains(formatted, "    MAC: i32 = 2") {
		t.Fatalf("expected formatter to write shorthand const module members, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "const WIN") || strings.Contains(formatted, "module OS:") && !strings.Contains(formatted, "const module OS:") {
		t.Fatalf("expected formatter not to rewrite const module members as top-level consts, got:\n%s", formatted)
	}
}

func TestParseScopeQualifiedModuleNamesAndCalls(t *testing.T) {
	file, errs := parseSourceFile(t, `module Ports::Launch:
    type Code = i32

    def exit_code() -> Code:
        return 7

def main() -> i32:
    return Ports::Launch::exit_code()
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	module, ok := file.Decls[0].(*ast.NamespaceDecl)
	if !ok {
		t.Fatalf("expected module decl, got %T", file.Decls[0])
	}
	if module.Name != "Ports.Launch" {
		t.Fatalf("expected scope-qualified module name to normalize to Ports.Launch, got %q", module.Name)
	}
	mainDecl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected main decl, got %T", file.Decls[1])
	}
	ret, ok := mainDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", mainDecl.Body[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", ret.Value)
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok {
		t.Fatalf("expected scope-qualified call target to parse as ident, got %T", call.Func)
	}
	if ident.Name != "Ports.Launch.exit_code" {
		t.Fatalf("expected normalized call target Ports.Launch.exit_code, got %q", ident.Name)
	}
}
