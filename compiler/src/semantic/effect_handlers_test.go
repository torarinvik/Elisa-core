package semantic

import (
	"strings"
	"testing"
)

func TestStaticEffectHandlerLowersOperationToDirectCall(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "static_effect_handler.elisa", `
effect Tick:
    def ping() -> void

handler Noop() for Tick:
    def ping() -> void:
        pass

def main() -> void:
    can Tick with Noop():
        Tick.ping()
`)
	mainSymbol, ok := result.GlobalScope.Lookup("main")
	if !ok {
		t.Fatal("expected main symbol")
	}
	mainType, ok := mainSymbol.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected main function type, got %T", mainSymbol.Type)
	}
	if got := PermissionRefsString(mainType.PermissionRefs); got != " can[Tick]" {
		t.Fatalf("expected abstract Tick to remain in the inferred effect row, got %q", got)
	}
	if _, ok := result.GlobalScope.Lookup(EffectHandlerMethodSymbolName("Noop", "ping")); !ok {
		t.Fatal("expected hidden direct handler operation symbol")
	}
	if strings.Contains(strings.Join(result.Errors(), "\n"), "requires an installed handler") {
		t.Fatal("a statically handled effect operation should not require a runtime handler")
	}
}

func TestAbstractEffectRequiresHandler(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "unhandled_effect.elisa", `
effect Tick:
    def ping() -> void

def main() -> void:
    Tick.ping()
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "requires an installed handler") {
		t.Fatalf("expected unhandled abstract effect diagnostic, got %v", result.Errors())
	}
}

func TestEffectViaKeepsAbstractAndConcretePermissions(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "effect_via.elisa", `
effect Writer[T]:
    def write(value: T) -> void

permission MyConsole:
    Write

def use() -> void can[Writer[sview] via MyConsole.Write]:
    pass
`)
	sym, ok := result.GlobalScope.Lookup("use")
	if !ok {
		t.Fatal("expected use symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected use function type, got %T", sym.Type)
	}
	got := PermissionRefsString(fnType.PermissionRefs)
	if !strings.Contains(got, "Writer[sview]") || !strings.Contains(got, "MyConsole.Write") {
		t.Fatalf("expected abstract effect and concrete realization to remain visible, got %q", got)
	}
}

func TestHandlerOperationCanGrantConcretePermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "handler_concrete_permission.elisa", `
permission LocalConsole:
    Write

effect Writer[T]:
    def write(value: T) -> void

handler ConsoleLines(stream: i64) for Writer[sview]:
    def write(value: sview) -> void can[LocalConsole.Write]:
        can LocalConsole.Write:
            pass

def main() -> void can[Writer[sview] via LocalConsole.Write]:
    can Writer[sview] with ConsoleLines(1):
        Writer.write("hello")
`)
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("expected concrete permission to stay inside the handler realization, got %v", errors)
	}
	if _, ok := result.GlobalScope.Lookup(EffectHandlerMethodSymbolName("ConsoleLines", "write")); !ok {
		t.Fatal("expected handler operation to remain a direct hidden function")
	}
}

func TestZeroOverheadTailResumeIsInferredAndErased(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "tail_resume.elisa", `
effect Tick:
    def ping() -> void

handler static Noop() for Tick:
    def ping() -> void:
        resume()

def main() -> void:
    can Tick with Noop:
        Tick.ping()
`)
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("expected tail resume to analyze cleanly, got %v", errors)
	}
	if _, ok := result.GlobalScope.Lookup(EffectHandlerMethodSymbolName("Noop", "ping")); !ok {
		t.Fatal("expected resumable operation to remain a direct hidden function")
	}
}

func TestZeroOverheadResumeRejectsNonTailShape(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "non_tail_resume.elisa", `
effect Tick:
    def ping() -> void

handler static Bad() for Tick:
    def ping() -> void:
        resume()
        return
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "must be exactly one final resume() statement") {
		t.Fatalf("expected non-tail resume diagnostic, got %v", result.Errors())
	}
}

func TestNestedStaticHandlersForwardMissingOperation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "nested_handler_forward.elisa", `
effect Tick:
    def ping() -> void
    def flush() -> void

handler Base() for Tick:
    def flush() -> void:
        pass

handler Inner() for Tick:
    def ping() -> void:
        pass

def main() -> void:
    can Tick with Base:
        can Tick with Inner:
            Tick.ping()
            Tick.flush()
`)
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("expected nested partial handlers to forward statically, got %v", errors)
	}
	if _, ok := result.GlobalScope.Lookup(EffectHandlerMethodSymbolName("Inner", "ping")); !ok {
		t.Fatal("expected inner direct operation symbol")
	}
	if _, ok := result.GlobalScope.Lookup(EffectHandlerMethodSymbolName("Base", "flush")); !ok {
		t.Fatal("expected enclosing direct operation symbol")
	}
}

func TestHandlerInstallationRequiresExactGenericSpecialization(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "handler_specialization_mismatch.elisa", `
effect Writer[T]:
    def write(value: T) -> void

handler TextSink() for Writer[sview]:
    def write(value: sview) -> void:
        pass

def main() -> void:
    can Writer[i64] with TextSink:
        Writer.write(1)
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "exact abstract effect specialization") {
		t.Fatalf("expected exact generic handler specialization diagnostic, got %v", result.Errors())
	}
}
