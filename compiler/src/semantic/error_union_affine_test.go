package semantic

import (
	"strings"
	"testing"
)

// An error union dropped as a bare statement is a swallowed error (and a leaked
// payload if the variant carries one). It must be handled or propagated.
func TestAnalyzeDroppedErrorUnionRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "dropped_error_union.elisa", `
error E:
	Bad

extern f() -> i64 error[E]

def g() -> void:
	f()
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "error union result must be handled") {
		t.Fatalf("expected dropped error union to be rejected; got: %s", got)
	}
}

// `_ = f()` discards the error union (swallows the error); rejected. `_ = try f() else …`
// (handle, then discard the ok value) is fine.
func TestAnalyzeDiscardedErrorUnionRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "discarded_error_union.elisa", `
error E:
	Bad

extern f() -> i64 error[E]

def g() -> void:
	_ = f()
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "discarding it with `_ =` swallows the error") {
		t.Fatalf("expected `_ =` discard of error union to be rejected; got: %s", got)
	}
}

func TestAnalyzeTryThenDiscardAccepted(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "try_then_discard.elisa", `
error E:
	Bad

extern f() -> i64 error[E]

def g() -> void:
	_ = try f() else 0
`)
}

// Handling via `try ... else` and propagating via `return try` / `return <union>`
// all discharge the obligation — no error.
func TestAnalyzeHandledOrPropagatedErrorUnionAccepted(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "handled_error_union.elisa", `
error E:
	Bad

extern f() -> i64 error[E]

def use_recovered(x: i64) -> void:
	_ = x

def handled() -> void:
	use_recovered(try f() else 0)

def propagated_try() -> i64 error[E]:
	return try f()

def propagated_direct() -> i64 error[E]:
	return f()
`)
}
