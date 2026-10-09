package semantic

import (
	"strings"
	"testing"
)

func TestConcreteProtocolCastSelectsByRequestedReturnType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "concrete_protocol_cast_target.elisa", `
protocol ToText:
    def __cast__(self: Self) -> cstr can[Console.Format]

protocol ToInt:
    def __cast__(self: Self) -> i64

struct Box[T]:
    text: cstr

impl[T] ToText for Box[T]:
    def __cast__(self: Box[T]) -> cstr can[Console.Format]:
        return self.text

impl[T] ToInt for Box[T]:
    def __cast__(self: Box[T]) -> i64:
        return 17

def as_text(value: Box[i32]) -> cstr:
    can Console.Format:
        return value.cstr()

def as_int(value: Box[i32]) -> i64:
    return value.i64()
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected target-named casts to select the matching return overload, got: %v", result.Errors())
	}
	sym, ok := result.GlobalScope.Lookup("as_text")
	if !ok {
		t.Fatal("expected as_text symbol")
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected as_text function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fn.PermissionRefs); got != " can[Console.Format]" {
		t.Fatalf("concrete protocol cast must retain its effect in the caller row, got %q", got)
	}
}

func TestConcreteProtocolCastPreservesExactGlobalHookPrecedence(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "concrete_protocol_cast_global_hook.elisa", `
protocol ToText:
    def __cast__(self: Self) -> cstr can[Console.Format]

struct Box[T]:
    text: cstr

impl[T] ToText for Box[T]:
    def __cast__(self: Box[T]) -> cstr can[Console.Format]:
        return self.text

def __cast__(value: Box[i32]) -> cstr:
    return value.text

def as_text(value: Box[i32]) -> cstr:
    return value.cstr()
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("expected exact global cast hook to retain precedence, got: %v", result.Errors())
	}
	sym, ok := result.GlobalScope.Lookup("as_text")
	if !ok {
		t.Fatal("expected as_text symbol")
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected as_text function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fn.PermissionRefs); got != "" {
		t.Fatalf("exact global cast hook should win over the effectful protocol impl, got caller row %q", got)
	}
}

func TestConcreteProtocolCastReportsAmbiguityOnlyForMatchingTarget(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "concrete_protocol_cast_ambiguous.elisa", `
protocol FirstText:
    def __cast__(self: Self) -> cstr

protocol SecondText:
    def __cast__(self: Self) -> cstr

protocol ToInt:
    def __cast__(self: Self) -> i64

struct Box[T]:
    text: cstr

impl[T] FirstText for Box[T]:
    def __cast__(self: Box[T]) -> cstr:
        return self.text

impl[T] SecondText for Box[T]:
    def __cast__(self: Box[T]) -> cstr:
        return self.text

impl[T] ToInt for Box[T]:
    def __cast__(self: Box[T]) -> i64:
        return 17

def as_text(value: Box[i32]) -> cstr:
    return value.cstr()

def as_int(value: Box[i32]) -> i64:
    return value.i64()
`, AnalyzeOptions{})
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "postfix cast from Box[i32] to cstr is ambiguous across multiple protocol impls") {
		t.Fatalf("expected ambiguity among only the matching cstr-returning impls, got:\n%s", joined)
	}
	if strings.Contains(joined, "postfix cast from Box[i32] to i64 is ambiguous") {
		t.Fatalf("different return-type overloads must not make the i64 target ambiguous:\n%s", joined)
	}
}

func TestConcreteProtocolCastEnforcesImplBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "concrete_protocol_cast_bound.elisa", `
protocol Marked:
    def mark(self: Self) -> void

struct Good:
    value: i64

impl Marked for Good:
    def mark(self: Good) -> void:
        return

protocol ToText:
    def __cast__(self: Self) -> cstr

struct Box[T]:
    text: cstr

impl[T: Marked] ToText for Box[T]:
    def __cast__(self: Box[T]) -> cstr:
        return self.text

def accepted(value: Box[Good]) -> cstr:
    return value.cstr()

def rejected(value: Box[i64]) -> cstr:
    return value.cstr()
`, AnalyzeOptions{})
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "invalid cast from Box[i64] to cstr") {
		t.Fatalf("expected a receiver violating the impl bound not to dispatch the cast method, got:\n%s", joined)
	}
	if strings.Contains(joined, "invalid cast from Box[Good] to cstr") {
		t.Fatalf("expected the bounded receiver to dispatch the cast method, got:\n%s", joined)
	}
}

func TestConcreteProtocolCastRequiresLocalGrantEvenWhenSignatureDeclaresIt(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "concrete_protocol_cast_effect_scope.elisa", `
protocol ToText:
    def __cast__(self: Self) -> cstr can[Console.Format]

struct Box[T]:
    text: cstr

impl[T] ToText for Box[T]:
    def __cast__(self: Box[T]) -> cstr can[Console.Format]:
        return self.text

def allowed(value: Box[i32]) -> cstr:
    can Console.Format:
        return value.cstr()

def denied_by_signature_only(value: Box[i32]) -> cstr can[Console.Format]:
    return value.cstr()
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, "explicit local effect grant") {
		t.Fatalf("expected the protocol cast to require a local grant even when the function signature declares it, got:\n%s", all)
	}
	if strings.Contains(all, "invalid cast from Box[i32] to cstr") {
		t.Fatalf("expected the concrete protocol cast itself to resolve, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("allowed")
	if !ok {
		t.Fatal("expected allowed symbol")
	}
	fn, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected allowed function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fn.PermissionRefs); got != " can[Console.Format]" {
		t.Fatalf("local concrete cast grant should remain in the inferred caller row, got %q", got)
	}
}
