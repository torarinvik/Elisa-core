package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func TestUnparseCanBlockDoesNotInlineNestedCanExpr(t *testing.T) {
	src := `extern g() -> i64 can[Memory.Allocate]

def f() -> i64:
    can Memory.Allocate:
        return g() can Memory.Allocate
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	formatted := unparse.FormatFile(file)
	if strings.Contains(formatted, "can Memory.Allocate can Memory.Allocate") {
		t.Fatalf("expected unparse not to duplicate nested can permissions, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "can Memory.Allocate:\n        return g() can Memory.Allocate") {
		t.Fatalf("expected unparse to keep block can around nested can expression, got:\n%s", formatted)
	}
	if _, errs := parseSourceFile(t, formatted); len(errs) != 0 {
		t.Fatalf("expected formatted output to parse cleanly, got %v\n%s", errs, formatted)
	}
}

func TestParseTrustedPermissionBlock(t *testing.T) {
	src := `extern raw_pointer_cast() -> i64 can[Unsafe.PointerCast]

def f() -> i64:
    trusted Unsafe.PointerCast:
        return raw_pointer_cast()
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[1].(*ast.FuncDecl)
	trusted, ok := fn.Body[0].(*ast.CanStmt)
	if !ok {
		t.Fatalf("expected trusted block to parse as permission grant stmt, got %T", fn.Body[0])
	}
	if !trusted.SuppressPermissionInference {
		t.Fatalf("expected trusted block to suppress permission inference")
	}
	if len(trusted.Permissions) != 1 || trusted.Permissions[0].Name != "Unsafe" || trusted.Permissions[0].Member != "PointerCast" {
		t.Fatalf("unexpected trusted permissions: %#v", trusted.Permissions)
	}
	formatted := strings.TrimSpace(unparse.FormatFile(file))
	if !strings.Contains(formatted, "trusted Unsafe.PointerCast:\n        return raw_pointer_cast()") {
		t.Fatalf("expected formatter to preserve trusted block, got:\n%s", formatted)
	}
	if _, errs := parseSourceFile(t, formatted); len(errs) != 0 {
		t.Fatalf("expected formatted trusted block to parse cleanly, got %v\n%s", errs, formatted)
	}
}

func TestParsePermissionDeclAndSignalStmt(t *testing.T) {
	src := `permission FooEffect:
    pass

permission ConsoleEffect:
    Write
    Flush

def run() -> void can[FooEffect, ConsoleEffect.Write]:
    signal FooEffect
    signal ConsoleEffect.Write
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	first, ok := file.Decls[0].(*ast.PermissionDecl)
	if !ok {
		t.Fatalf("expected permission decl, got %T", file.Decls[0])
	}
	if first.Name != "FooEffect" || len(first.Members) != 0 {
		t.Fatalf("unexpected marker permission decl: %#v", first)
	}
	second, ok := file.Decls[1].(*ast.PermissionDecl)
	if !ok {
		t.Fatalf("expected second compatibility permission decl, got %T", file.Decls[1])
	}
	if len(second.Members) != 2 || second.Members[0] != "Write" || second.Members[1] != "Flush" {
		t.Fatalf("unexpected members: %#v", second.Members)
	}
	fn := file.Decls[2].(*ast.FuncDecl)
	firstSignal, ok := fn.Body[0].(*ast.SignalStmt)
	if !ok {
		t.Fatalf("expected signal stmt, got %T", fn.Body[0])
	}
	if got := firstSignal.Permissions[0]; got.Name != "FooEffect" || got.Member != "" {
		t.Fatalf("unexpected first signal permission: %#v", got)
	}
	secondSignal := fn.Body[1].(*ast.SignalStmt)
	if got := secondSignal.Permissions[0]; got.Name != "ConsoleEffect" || got.Member != "Write" {
		t.Fatalf("unexpected second signal permission: %#v", got)
	}
}

func TestUnparsePermissionDeclAndSignalStmt(t *testing.T) {
	src := `permission FooEffect:
    pass

permission ConsoleEffect:
    Write
    Flush

def run() -> void can[FooEffect, ConsoleEffect.Write]:
    signal FooEffect
    signal ConsoleEffect.Write
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	got := strings.TrimSpace(unparse.FormatFile(file))
	want := `permission FooEffect:
    pass

permission ConsoleEffect:
    Write
    Flush

def run() -> void can[FooEffect, ConsoleEffect.Write]:
    signal FooEffect
    signal ConsoleEffect.Write`
	if got != strings.TrimSpace(want) {
		t.Fatalf("unexpected unparse output:\n%s", got)
	}
}
