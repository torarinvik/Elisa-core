package semantic_test

import (
	"strings"
	"testing"
)

// `return void_call()` in a void function is legal (tail-style return): void is
// assignable to void, the call runs for its side effects.
func TestAnalyzeAcceptsReturnOfVoidCallInVoidFunction(t *testing.T) {
	src := `def helper() -> void:
    return

def v() -> void:
    return helper()
`
	_, errs := parseAndAnalyze(t, "return_void_call_ok.elisa", src)
	if len(errs) != 0 {
		t.Fatalf("expected no semantic errors, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A non-void value in a void return still gets the proper diagnostic.
func TestAnalyzeRejectsNonVoidReturnValueInVoidFunction(t *testing.T) {
	src := `def v() -> void:
    return 5
`
	_, errs := parseAndAnalyze(t, "return_value_in_void_fn.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects void, got int") {
		t.Fatalf("expected void return-type diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Returning a void call from a value-returning function is a type mismatch.
func TestAnalyzeRejectsReturnOfVoidCallInValueFunction(t *testing.T) {
	src := `def helper() -> void:
    return

def f() -> i64:
    return helper()
`
	_, errs := parseAndAnalyze(t, "return_void_call_in_value_fn.elisa", src)
	if !strings.Contains(strings.Join(errs, "\n"), "return type expects i64, got void") {
		t.Fatalf("expected i64 return-type diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
