package semantic

import (
	"strings"
	"testing"
)

// Phase 5b: a `[errorset R]` combinator propagates the callback's EXACT error
// set, so the same generic function works for two different error types.
func TestErrorSetParamPropagatesCallbackSet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "esp_propagate.elisa", `
error IoErr:
    Bad

error NetErr:
    Down

extern reader() -> i64 error[IoErr]
extern fetch() -> i64 error[NetErr]

def applies[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    return try f()

def useIo() -> i64 error[IoErr]:
    return applies(reader)

def useNet() -> i64 error[NetErr]:
    return applies(fetch)
`)
	all := allDiagnostics(result)
	if strings.TrimSpace(all) != "" {
		t.Fatalf("expected error-set-polymorphic combinator to type-check for both IoErr and NetErr, got:\n%s", all)
	}
}

// The propagated set is PRECISE: feeding an IoErr callback through the
// combinator and then requiring NetErr at the use site must be rejected.
func TestErrorSetParamPrecise(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "esp_precise.elisa", `
error IoErr:
    Bad

error NetErr:
    Down

extern reader() -> i64 error[IoErr]

def applies[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    return try f()

def use() -> i64 error[NetErr]:
    return applies(reader)
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, "IoErr") || !strings.Contains(all, "NetErr") {
		t.Fatalf("expected applies(reader) to yield error[IoErr], rejected against error[NetErr], got:\n%s", all)
	}
}

// A non-fallible callback gives R nothing to bind to → reported, not silently
// accepted.
func TestErrorSetParamUnbound(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "esp_unbound.elisa", `
extern plain() -> i64

def applies[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    return try f()

def use() -> i64:
    return applies(plain)
`)
	all := allDiagnostics(result)
	if !strings.Contains(all, "error-set parameter") {
		t.Fatalf("expected `cannot infer error-set parameter R`, got:\n%s", all)
	}
}

// Two error-set params stay distinct (no cross-binding).
func TestErrorSetParamsDistinct(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "esp_two.elisa", `
error IoErr:
    Bad

error NetErr:
    Down

extern reader() -> i64 error[IoErr]
extern fetch() -> i64 error[NetErr]

def combine[errorset R, errorset S](f: func() -> i64 error[R], g: func() -> i64 error[S]) -> i64 error[R]:
    return try f()

def use() -> i64 error[IoErr]:
    return combine(reader, fetch)
`)
	all := allDiagnostics(result)
	if strings.TrimSpace(all) != "" {
		t.Fatalf("expected two distinct error-set params to bind independently (return error[IoErr]), got:\n%s", all)
	}
}
