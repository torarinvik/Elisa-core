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

func TestErrorUnionReturnRequiresExplicitMoveForAffineOkPayload(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "error_union_affine_ok_payload.elisa", `error E:
    Bad

linear struct Guard:
    open: bool

def make() -> Guard:
    return Guard(true)

def wrap() -> Guard error[E]:
    g: mutable Guard = make()
    return g
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "must be moved explicitly") {
		t.Fatalf("expected affine ok payload to require explicit move into error union return; got:\n%s", all)
	}
}

func TestErrorUnionReturnConsumesExplicitMoveForAffineOkPayload(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "error_union_affine_ok_payload_move.elisa", `error E:
    Bad

linear struct Guard:
    open: bool

def make() -> Guard:
    return Guard(true)

def wrap() -> Guard error[E]:
    g: mutable Guard = make()
    return move g
`)
	if all := strings.Join(result.Errors(), "\n"); all != "" {
		t.Fatalf("explicit move into error union return should analyze cleanly; got:\n%s", all)
	}
}

func TestCatchConsumesStoredErrorUnionWithAffineOkPayload(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "error_union_affine_catch_consumes.elisa", `error E:
    Bad

affine struct Tok:
    n: i64

def wrap() -> Tok error[E]:
    t: mutable Tok = Tok(1)
    return move t

def f() -> void:
    u: mutable Tok error[E] = wrap()
    catch u:
        ok:
            ok.n
        E.Bad:
            0
    catch u:
        ok:
            ok.n
        E.Bad:
            0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "cannot be used") {
		t.Fatalf("expected stored error union to be consumed by catch; got:\n%s", all)
	}
}

func TestTryConsumesStoredErrorUnionWithAffineOkPayload(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "error_union_affine_try_consumes.elisa", `error E:
    Bad

affine struct Tok:
    n: i64

def wrap() -> Tok error[E]:
    t: mutable Tok = Tok(1)
    return move t

def f() -> i64:
    u: mutable Tok error[E] = wrap()
    a: Tok = try u else Tok(2)
    b: Tok = try u else Tok(3)
    return a.n + b.n
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "cannot be used") {
		t.Fatalf("expected stored error union to be consumed by try; got:\n%s", all)
	}
}
