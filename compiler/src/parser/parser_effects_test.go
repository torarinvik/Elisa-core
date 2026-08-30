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

func TestParseAbstractEffectAndStaticHandler(t *testing.T) {
	src := `effect Writer[T]:
    def write(value: T) -> void

permission Console:
    Write

handler ConsoleLines(stream: Stream) for Writer[sview]:
    def write(value: sview) -> void can[Console.Write]:
        Console.write(stream, value)
        Console.write(stream, "\n")

def main() -> void can[Writer[sview] via Console.Write]:
    can Writer[sview] with ConsoleLines(stdout):
        Writer.write("hello")
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	if len(file.Decls) != 4 {
		t.Fatalf("expected effect, handler, and function declarations, got %d: %#v", len(file.Decls), file.Decls)
	}
	effect, ok := file.Decls[0].(*ast.InterfaceDecl)
	if !ok || !effect.IsEffect || len(effect.GenericParams) != 1 || len(effect.Members) != 1 {
		t.Fatalf("unexpected effect declaration: %#v", file.Decls[0])
	}
	handler, ok := file.Decls[2].(*ast.ImplDecl)
	if !ok || !handler.IsHandler || handler.HandlerName != "ConsoleLines" || len(handler.HandlerParams) != 1 {
		t.Fatalf("unexpected handler declaration: %#v", file.Decls[2])
	}
	method := handler.Members[0].(*ast.FuncDecl)
	if len(method.Params) != 2 || method.Params[0].Name != "stream" {
		t.Fatalf("expected captured handler parameter to be prepended, got %#v", method.Params)
	}
	mainFn := file.Decls[3].(*ast.FuncDecl)
	if len(mainFn.Permissions) != 1 || len(mainFn.Permissions[0].TypeArgs) != 1 || len(mainFn.Permissions[0].Via) != 1 {
		t.Fatalf("unexpected abstract effect realization: %#v", mainFn.Permissions)
	}
	can := mainFn.Body[0].(*ast.CanStmt)
	if can.HandlerName != "ConsoleLines" || len(can.HandlerArgs) != 1 {
		t.Fatalf("unexpected handler installation: %#v", can)
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "effect Writer[T]:") || !strings.Contains(formatted, "handler ConsoleLines(stream: Stream) for Writer[sview]:") || !strings.Contains(formatted, "with ConsoleLines(stdout):") {
		t.Fatalf("formatter lost effect syntax:\n%s", formatted)
	}
	if _, errs := parseSourceFile(t, formatted); len(errs) != 0 {
		t.Fatalf("expected formatted effect program to parse cleanly, got %v\n%s", errs, formatted)
	}
}

func TestParseStaticHandlerWithoutCaptureArguments(t *testing.T) {
	src := `effect Tick:
    def ping() -> void

handler Noop() for Tick:
    def ping() -> void:
        return

def main() -> i64:
    can Tick with Noop:
        Tick.ping()
    return 42
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	mainFn := file.Decls[2].(*ast.FuncDecl)
	can := mainFn.Body[0].(*ast.CanStmt)
	if can.HandlerName != "Noop" || len(can.HandlerArgs) != 0 {
		t.Fatalf("unexpected zero-capture handler installation: %#v", can)
	}
}

func TestParseZeroOverheadHandlerInfersTailResume(t *testing.T) {
	src := `effect Tick:
    def ping() -> void

handler static Noop() for Tick:
    def ping() -> void:
        resume()

def main() -> i64:
    can Tick with Noop:
        Tick.ping()
    return 42
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	handler, ok := file.Decls[1].(*ast.ImplDecl)
	if !ok || !handler.IsHandler || !handler.HandlerStatic {
		t.Fatalf("expected static handler metadata, got %#v", file.Decls[1])
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "handler static Noop() for Tick:") || strings.Contains(formatted, "one_shot") {
		t.Fatalf("expected unparse to preserve handler mode, got:\n%s", formatted)
	}
	if _, errs := parseSourceFile(t, formatted); len(errs) != 0 {
		t.Fatalf("expected formatted zero-overhead handler to parse cleanly, got %v\n%s", errs, formatted)
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
