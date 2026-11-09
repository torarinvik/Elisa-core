package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeValidInlineProgram(t *testing.T) {
	// The extern states the capability of the reference it returns: `box` writes through it, so it
	// is `mutable Box&?`. A read-only `Box&?` would only make the `mutable` binding rebindable.
	src := `struct Box:
    value: mutable int

extern make_box() -> mutable Box&?

def read_box() -> int:
	box: mutable Box&? = make_box()
    if box == null:
        return 0
    box.value <- 7
    return box.value
`
	_, errs := parseAndAnalyze(t, "inline_valid.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeUndefinedIdentifier(t *testing.T) {
	src := `def bad() -> int:
    return missing
`
	_, errs := parseAndAnalyze(t, "undefined_ident.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(errs[0], "undefined identifier") {
		t.Fatalf("expected undefined identifier error, got %q", errs[0])
	}
}
func TestAnalyzeWrongCallArity(t *testing.T) {
	src := `extern alloc(size: usize) -> int

def use_alloc() -> int:
    return alloc()
`
	_, errs := parseAndAnalyze(t, "wrong_arity.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects 1 arguments, got 0") {
		t.Fatalf("expected arity diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsFunctionTypeParametersAndHigherOrderCalls(t *testing.T) {
	src := `def apply_identity[T](fn: fn(T) -> T, value: T) -> T:
    return fn(value)

def bump(value: int) -> int:
    return value + 1

def run() -> int:
	return apply_identity(bump, 41)
`
	result, errs := parseAndAnalyze(t, "higher_order_function_types.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "apply_identity", "T")
	requireFunctionReturnTypeString(t, result, "run", "int")
}
func TestAnalyzeRejectsAssigningFunctionWithNarrowerParamType(t *testing.T) {
	src := `struct Box:
	value: int

def only_nonnull(box: Box&) -> int:
	return box.value

def bad() -> int:
	wider: fn(Box&?) -> int = only_nonnull
	return wider(null)
`
	_, errs := parseAndAnalyze(t, "function_param_contravariance_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, `variable "wider" expects`) || !strings.Contains(all, `fn(Box&?) -> int`) || !strings.Contains(all, `fn(Box&) -> int`) {
		t.Fatalf("expected contravariant function assignment diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsAssigningFunctionWithBroaderParamType(t *testing.T) {
	src := `struct Box:
	value: int

def allow_null(box: Box&?) -> int:
	if box == null:
		return 0
	return box.value

def ok() -> int:
	region scratch(256)
	box: Box& = new[scratch] Box{value: 7}
	narrower: fn(Box&) -> int = allow_null
	return narrower(box)
`
	result, errs := parseAndAnalyze(t, "function_param_contravariance_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "int")
}
