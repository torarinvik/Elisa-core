package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

func TestParseEffectsDeclAndAliasUsage(t *testing.T) {
	src := `effectalias FrontendEffects = error[ParseErr] can[Abort.Panic, Memory.Allocate]
def parse() -> int effects[FrontendEffects]:
    return 1
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	effectsDecl, ok := file.Decls[0].(*ast.EffectsDecl)
	if !ok {
		t.Fatalf("expected effects decl, got %T", file.Decls[0])
	}
	if effectsDecl.Name != "FrontendEffects" {
		t.Fatalf("expected alias name FrontendEffects, got %q", effectsDecl.Name)
	}
	if effectsDecl.ErrorEffects == nil || len(effectsDecl.Permissions) != 2 {
		t.Fatalf("expected both error and can clauses, got %#v", effectsDecl)
	}
	fn, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	if len(fn.Effects) != 1 || fn.Effects[0].Alias != "FrontendEffects" {
		t.Fatalf("expected func effect alias FrontendEffects, got %#v", fn.Effects)
	}
	if len(fn.Permissions) != 0 {
		t.Fatalf("expected alias-using function to have no explicit can clause, got %#v", fn.Permissions)
	}
	if _, ok := fn.ReturnType.(*ast.ErrorUnionTypeExpr); ok {
		t.Fatalf("expected parser to preserve plain return type when alias is used, got %#v", fn.ReturnType)
	}
}

func TestParseFuncTypeWithEffectsAlias(t *testing.T) {
	file, errs := parseSourceFile(t, "extern register(callback: func() -> void effects[WorkerEffects]) -> void\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.ExternFuncDecl)
	fnType, ok := decl.Params[0].Type.(*ast.FuncTypeExpr)
	if !ok {
		t.Fatalf("expected func type expr, got %T", decl.Params[0].Type)
	}
	if len(fnType.Effects) != 1 || fnType.Effects[0].Alias != "WorkerEffects" {
		t.Fatalf("expected function type alias WorkerEffects, got %#v", fnType.Effects)
	}
}

func TestParseEffectsAliasRejectsMixedExplicitClauses(t *testing.T) {
	_, errs := parseSourceFile(t, "def parse() -> int error[ParseErr] effects FrontendEffects can[Abort.Panic]:\n    return 1\n")
	if len(errs) == 0 {
		t.Fatal("expected parser error for mixing effects alias with explicit clauses")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "effects alias cannot be combined") {
		t.Fatalf("expected mixed-clause parser error, got %v", errs)
	}
}

func TestParseBracketedEffectsCanMixAliasErrorsAndPermissions(t *testing.T) {
	src := `def parse() -> int effects[FrontendEffects, error ParseErr, Abort.Panic, Console.Write]:
    return 1
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	if len(fn.Effects) != 4 {
		t.Fatalf("expected 4 effect items, got %#v", fn.Effects)
	}
	if fn.Effects[0].Alias != "FrontendEffects" || fn.Effects[1].ErrorEffects == nil || fn.Effects[2].Permission == nil || fn.Effects[3].Permission == nil {
		t.Fatalf("unexpected effect item shape: %#v", fn.Effects)
	}
}

func TestUnparseEffectsAliasSyntax(t *testing.T) {
	src := `effectalias FrontendEffects = error[ParseErr] can[Abort.Panic, Memory.Allocate]

def parse() -> int effects[FrontendEffects]:
    return 1

extern register(callback: func() -> void effects[FrontendEffects]) -> void
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	got := strings.TrimSpace(unparse.FormatFile(file))
	if got != strings.TrimSpace(src) {
		t.Fatalf("unexpected unparse output:\n%s", got)
	}
}

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

func TestParseEffectDeclAndSignalStmt(t *testing.T) {
	src := `effect FooEffect: pass
effect ConsoleEffect: Write Flush

def run() -> void can[FooEffect, ConsoleEffect.Write]:
    signal FooEffect
    signal ConsoleEffect.Write
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	first, ok := file.Decls[0].(*ast.EffectDecl)
	if !ok {
		t.Fatalf("expected effect decl, got %T", file.Decls[0])
	}
	if first.Name != "FooEffect" || len(first.Members) != 0 {
		t.Fatalf("unexpected marker effect decl: %#v", first)
	}
	second, ok := file.Decls[1].(*ast.EffectDecl)
	if !ok {
		t.Fatalf("expected second effect decl, got %T", file.Decls[1])
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

func TestUnparseEffectDeclAndSignalStmt(t *testing.T) {
	src := `effect FooEffect:
    pass

effect ConsoleEffect:
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
	if got != strings.TrimSpace(src) {
		t.Fatalf("unexpected unparse output:\n%s", got)
	}
}
