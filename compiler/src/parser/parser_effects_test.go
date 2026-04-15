package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/unparse"
)

func TestParseEffectsDeclAndAliasUsage(t *testing.T) {
	src := `effects FrontendEffects = error[ParseErr] can[Abort.Panic, Memory.Allocate]
def parse() -> int effects FrontendEffects:
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
	if fn.EffectAlias != "FrontendEffects" {
		t.Fatalf("expected func effect alias FrontendEffects, got %q", fn.EffectAlias)
	}
	if len(fn.Permissions) != 0 {
		t.Fatalf("expected alias-using function to have no explicit can clause, got %#v", fn.Permissions)
	}
	if _, ok := fn.ReturnType.(*ast.ErrorUnionTypeExpr); ok {
		t.Fatalf("expected parser to preserve plain return type when alias is used, got %#v", fn.ReturnType)
	}
}

func TestParseFuncTypeWithEffectsAlias(t *testing.T) {
	file, errs := parseSourceFile(t, "extern register(callback: func() -> void effects WorkerEffects) -> void\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.ExternFuncDecl)
	fnType, ok := decl.Params[0].Type.(*ast.FuncTypeExpr)
	if !ok {
		t.Fatalf("expected func type expr, got %T", decl.Params[0].Type)
	}
	if fnType.EffectAlias != "WorkerEffects" {
		t.Fatalf("expected function type alias WorkerEffects, got %q", fnType.EffectAlias)
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

func TestUnparseEffectsAliasSyntax(t *testing.T) {
	src := `effects FrontendEffects = error[ParseErr] can[Abort.Panic, Memory.Allocate]

def parse() -> int effects FrontendEffects:
    return 1

extern register(callback: func() -> void effects FrontendEffects) -> void
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
