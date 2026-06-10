package semantic

import (
	"strings"
	"testing"
)

func TestPermissionDeclAndSignalInferPermissions(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "permission_signal.elisa", `
permission FooEffect: pass
permission ConsoleEffect:
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
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "effect_signal_unknown.elisa", `
def run() -> void:
    signal MissingEffect
`)
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, UnknownPermissionMessage("MissingEffect")) {
		t.Fatalf("expected unknown signal effect error, got %v", result.Errors())
	}
}
