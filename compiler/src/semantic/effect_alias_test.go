package semantic

import (
	"strings"
	"testing"
)

func TestEffectAliasExpandsReturnAndPermissions(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "effect_alias.llcontext", `
error ParseErr:
    Invalid

effectalias FrontendEffects = error[ParseErr] can[Abort.Panic, Memory.Allocate]

def parse() -> int effects[FrontendEffects]:
    return 1
`)
	sym, ok := result.GlobalScope.Lookup("parse")
	if !ok {
		t.Fatal("expected parse symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", sym.Type)
	}
	ret, ok := fnType.Return.(*ErrorUnionType)
	if !ok {
		t.Fatalf("expected error union return, got %T", fnType.Return)
	}
	if ret.Value.String() != "int" {
		t.Fatalf("expected int payload, got %s", ret.Value.String())
	}
	if ret.Errors == nil || ret.Errors.Name != "ParseErr" {
		t.Fatalf("expected ParseErr error set, got %#v", ret.Errors)
	}
	if got := PermissionRefsString(fnType.DeclaredPermissionRefs); got != " can[Abort.Panic, Memory.Allocate]" {
		t.Fatalf("unexpected declared permissions: %q", got)
	}
}

func TestEffectAliasResolvesThroughUsingAndFuncTypes(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "effect_alias_using.llcontext", `
namespace frontend:
    effectalias WorkerEffects = can[Abort.Panic]

using frontend

def accept(callback: func() -> void effects[WorkerEffects]) -> void:
    callback()
`)
	sym, ok := result.GlobalScope.Lookup("accept")
	if !ok {
		t.Fatal("expected accept symbol")
	}
	fnType := sym.Type.(*FuncType)
	callbackType, ok := fnType.Params[0].(*FuncType)
	if !ok {
		t.Fatalf("expected callback func type, got %T", fnType.Params[0])
	}
	if got := PermissionRefsString(callbackType.DeclaredPermissionRefs); got != " can[Abort.Panic]" {
		t.Fatalf("unexpected callback permissions: %q", got)
	}
}

func TestEffectAliasUnknownNameErrors(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "effect_alias_unknown.llcontext", `
def parse() -> int effects[MissingEffects]:
    return 1
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, `unknown permission "MissingEffects"`) {
		t.Fatalf("expected unknown effect item error, got %v", result.Errors())
	}
}

func TestEffectsRowMixesAliasErrorsAndPermissions(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "effect_row.llcontext", `
error ParseErr:
    Invalid

error IoErr:
    Failed

effectalias FrontendEffects = error[ParseErr] can[Abort.Panic]

def parse() -> int effects[FrontendEffects, error IoErr, Memory.Allocate]:
    return 1
`)
	sym, ok := result.GlobalScope.Lookup("parse")
	if !ok {
		t.Fatal("expected parse symbol")
	}
	fnType := sym.Type.(*FuncType)
	if _, ok := fnType.Return.(*ErrorUnionType); !ok {
		t.Fatalf("expected error union return, got %T", fnType.Return)
	}
	if got := PermissionRefsString(fnType.DeclaredPermissionRefs); got != " can[Abort.Panic, Memory.Allocate]" {
		t.Fatalf("unexpected declared permissions: %q", got)
	}
}

func TestEffectDeclAndSignalInferPermissions(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "effect_signal.llcontext", `
effect FooEffect: pass
effect ConsoleEffect:
    Write
    Flush

def run() -> void:
    signal FooEffect
    signal ConsoleEffect.Write
`)
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[ConsoleEffect.Write, FooEffect]" {
		t.Fatalf("unexpected inferred permissions: %q", got)
	}
}

func TestSignalUnknownEffectErrors(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "effect_signal_unknown.llcontext", `
def run() -> void:
    signal MissingEffect
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, UnknownPermissionMessage("MissingEffect")) {
		t.Fatalf("expected unknown signal effect error, got %v", result.Errors())
	}
}
