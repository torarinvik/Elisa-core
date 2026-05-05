package parser

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestParseEnumMapLowersToFunction(t *testing.T) {
	file, errs := parseSourceFile(t, "enum map routine_kind: TokenKind -> RoutineKind:\n    NEW => NEW\n    STANDARD_ASSIGN => ASSIGN\n    _ => RESET\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 1 {
		t.Fatalf("expected one decl, got %d", len(file.Decls))
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected enum map to lower to function, got %T", file.Decls[0])
	}
	if decl.Name != "routine_kind" {
		t.Fatalf("expected generated function name routine_kind, got %q", decl.Name)
	}
	if len(decl.Params) != 1 || decl.Params[0].Name != "value" {
		t.Fatalf("expected generated value parameter, got %#v", decl.Params)
	}
	if len(decl.Body) != 3 {
		t.Fatalf("expected two map tests plus fallback return, got %d statements", len(decl.Body))
	}
	first, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected first generated stmt to be if, got %T", decl.Body[0])
	}
	cond, ok := first.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != lexer.TOKEN_EQEQ {
		t.Fatalf("expected equality condition, got %#v", first.Cond)
	}
	member, ok := cond.Right.(*ast.FieldExpr)
	if !ok || member.Field != "NEW" {
		t.Fatalf("expected source member TokenKind.NEW, got %#v", cond.Right)
	}
	qual, ok := member.Object.(*ast.Ident)
	if !ok || qual.Name != "TokenKind" {
		t.Fatalf("expected source member qualifier TokenKind, got %#v", member.Object)
	}
	fallback, ok := decl.Body[2].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected fallback return, got %T", decl.Body[2])
	}
	fallbackMember, ok := fallback.Value.(*ast.FieldExpr)
	if !ok || fallbackMember.Field != "RESET" {
		t.Fatalf("expected fallback target RoutineKind.RESET, got %#v", fallback.Value)
	}
}
