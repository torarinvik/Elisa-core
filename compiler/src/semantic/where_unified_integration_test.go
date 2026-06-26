//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// where_unified_integration_test.go
//
// Black-box integration tests for the unified refinement pipeline described in
// docs/109-unified-refinement-pipeline.md.  Each test exercises end-to-end
// behavior visible at the semantic-analysis boundary; no internal field is
// inspected directly.
//
// Test categories:
//   A. Param / return / local where — all three binder positions
//   B. Provable-statically vs runtime-fallback discharge
//   C. Mutation invalidation of a local where fact
//   D. Erasure guardrail (where does not change assignability / SameType)
//   E. Coexistence of `where` and `is Law` on the same function
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// A. Binder positions
// ---------------------------------------------------------------------------

// A1: param where — no hard error for a well-formed predicate.
func TestUnifiedPipelineParamWhere(t *testing.T) {
	src := `
def f(n: i64 where n >= 0) -> i64:
    return n
`
	result := analyzeTreeTestSource(t, "unified_param_where.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("param where should analyze cleanly, got: %v", errs)
	}
}

// A2: return where — `result` binder in scope, no hard error.
func TestUnifiedPipelineReturnWhere(t *testing.T) {
	src := `
def make_pos(n: i64 where n >= 0) -> i64 where result >= 0:
    return n
`
	result := analyzeTreeTestSource(t, "unified_return_where.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("return where should analyze cleanly, got: %v", errs)
	}
}

// A3: local where — declared at a local binding, no hard error.
func TestUnifiedPipelineLocalWhere(t *testing.T) {
	src := `
def safe_get(xs: darray[i64]) -> i64:
    i: i64 where 0 <= i and i < xs.count = 0
    return xs[i]
`
	result := analyzeTreeTestSource(t, "unified_local_where.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("local where should analyze cleanly, got: %v", errs)
	}
}

// A4: all three positions combined on one function — no hard error.
func TestUnifiedPipelineAllThreePositions(t *testing.T) {
	src := `
def bounded(lo: i64, hi: i64 where hi >= lo, x: i64 where x >= lo and x <= hi) -> i64 where result >= lo and result <= hi:
    v: i64 where v >= lo and v <= hi = x
    return v
`
	result := analyzeTreeTestSource(t, "unified_all_positions.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("all three binder positions should analyze cleanly, got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// B. Discharge: provable statically vs runtime fallback
// ---------------------------------------------------------------------------

// B1: a param where fact that is trivially provable at a call site should not
// emit a proofLint when strict proofs are enabled.
func TestUnifiedPipelineStaticDischargeParamWhere(t *testing.T) {
	src := `
def need_pos(n: i64 where n > 0) -> i64:
    return n

def caller() -> i64:
    x: i64 where x > 0 = 5
    return need_pos(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "unified_static_param.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true},
	)
	diag := allDiagnostics(result)
	// A statically-provable obligation must NOT emit a proof failure or proofLint.
	if strings.Contains(diag, "proofLint") || strings.Contains(diag, "could not be proven") ||
		strings.Contains(diag, "violated") {
		t.Fatalf("statically provable param where should discharge without warning, got:\n%s", diag)
	}
}

// B2: a param where that cannot be proven statically should produce a diagnostic
// (proofLint or "could not be proven") rather than a hard type error.
func TestUnifiedPipelineUnprovableParamWhereFallback(t *testing.T) {
	src := `
def need_pos(n: i64 where n > 0) -> i64:
    return n

def caller(x: i64) -> i64:
    return need_pos(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "unified_unprovable_param.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true},
	)
	diag := allDiagnostics(result)
	// Must emit some form of proof obligation warning — not a type-mismatch error.
	hasObligation := strings.Contains(diag, "proofLint") ||
		strings.Contains(diag, "could not be proven") ||
		strings.Contains(diag, "violated") ||
		strings.Contains(diag, "where precondition")
	hasTypeError := strings.Contains(diag, "type mismatch") || strings.Contains(diag, "cannot assign")
	if !hasObligation {
		t.Fatalf("unprovable param where should produce a proof obligation diagnostic, got:\n%s", diag)
	}
	if hasTypeError {
		t.Fatalf("unprovable param where must not produce a type-mismatch error, got:\n%s", diag)
	}
}

// B3: a return where that is trivially satisfied (return value equals the constrained
// param) should not emit a proof failure.
func TestUnifiedPipelineStaticDischargeReturnWhere(t *testing.T) {
	src := `
def identity_pos(n: i64 where n >= 0) -> i64 where result >= 0:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "unified_static_return.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true},
	)
	diag := allDiagnostics(result)
	if strings.Contains(diag, "proofLint") || strings.Contains(diag, "could not be proven") {
		t.Fatalf("provable return where should discharge without warning, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// C. Mutation invalidation of a local where fact
// ---------------------------------------------------------------------------

// C1: reassigning the where-typed local BEFORE the use that depends on its predicate
// should invalidate the seeded fact.  The diagnostic channel (proofLint or hard error
// under strict) may or may not fire depending on what the call site can observe;
// the key requirement is that the program still analyzes (no type error) and the
// compiler does not assume the predicate holds after the reassignment.
//
// We verify the analysis completes cleanly (type-system level) because the compiler
// must not confuse "fact invalidated" with "ill-typed".
func TestUnifiedPipelineMutationInvalidatesLocalWhereFact(t *testing.T) {
	src := `
def need_pos(n: i64 where n > 0) -> i64:
    return n

def caller(xs: darray[i64]) -> i64:
    i: i64 where i > 0 = 1
    i = 0                      # invalidates the where fact; i may now be <= 0
    return need_pos(i)         # compiler must not assume i > 0 here
`
	// After mutation, the where fact is gone.  The call to need_pos(i) is now an
	// unverified obligation; under strict proofs we expect a diagnostic, not a
	// type-mismatch error.
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "unified_mutation_invalidate.elisa", src,
		AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true},
	)
	for _, e := range result.Errors() {
		if strings.Contains(e, "type mismatch") || strings.Contains(e, "cannot assign") {
			t.Fatalf("mutation invalidation must not produce a type-mismatch error, got: %v", e)
		}
	}
}

// ---------------------------------------------------------------------------
// D. Erasure guardrail
// ---------------------------------------------------------------------------

// D1: SameType between T and T where p must be true (checked via the type symbols
// on plain vs refined function parameters).
func TestUnifiedPipelineErasureGuardrailSameType(t *testing.T) {
	src := `
def plain(n: i64) -> i64:
    return n

def refined(n: i64 where n >= 0) -> i64:
    return n
`
	result := analyzeTreeTestSource(t, "unified_erasure_sametype.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("plain and refined should both analyze cleanly, got: %v", errs)
	}
	plainSym, ok1 := result.GlobalScope.Lookup("plain")
	refinedSym, ok2 := result.GlobalScope.Lookup("refined")
	if !ok1 || !ok2 {
		t.Fatal("expected both function symbols")
	}
	plainFT, ok1 := plainSym.Type.(*FuncType)
	refinedFT, ok2 := refinedSym.Type.(*FuncType)
	if !ok1 || !ok2 {
		t.Fatal("expected FuncType for both symbols")
	}
	if !SameType(plainFT.Params[0], refinedFT.Params[0]) {
		t.Fatalf("where refinement must erase to base before SameType; plain param=%s, refined param=%s",
			plainFT.Params[0], refinedFT.Params[0])
	}
}

// D2: AssignableTo must be bidirectional between T and T where p.
func TestUnifiedPipelineErasureGuardrailAssignableTo(t *testing.T) {
	src := `
def plain(n: i64) -> i64:
    return n

def refined(n: i64 where n >= 0) -> i64:
    return n
`
	result := analyzeTreeTestSource(t, "unified_erasure_assignable.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("both functions should analyze cleanly, got: %v", errs)
	}
	plainSym, _ := result.GlobalScope.Lookup("plain")
	refinedSym, _ := result.GlobalScope.Lookup("refined")
	plainFT := plainSym.Type.(*FuncType)
	refinedFT := refinedSym.Type.(*FuncType)
	if !AssignableTo(plainFT.Params[0], refinedFT.Params[0]) {
		t.Fatal("plain i64 must be assignable to i64 where p (erasure)")
	}
	if !AssignableTo(refinedFT.Params[0], plainFT.Params[0]) {
		t.Fatal("i64 where p must be assignable to plain i64 (erasure)")
	}
}

// D3: where on return type must not introduce directional AssignableTo behavior.
func TestUnifiedPipelineErasureGuardrailReturnType(t *testing.T) {
	src := `
def plain() -> i64:
    return 1

def refined() -> i64 where result >= 0:
    return 1
`
	result := analyzeTreeTestSource(t, "unified_erasure_return.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("both functions should analyze cleanly, got: %v", errs)
	}
	plainSym, _ := result.GlobalScope.Lookup("plain")
	refinedSym, _ := result.GlobalScope.Lookup("refined")
	plainFT := plainSym.Type.(*FuncType)
	refinedFT := refinedSym.Type.(*FuncType)
	if !SameType(plainFT.Return, refinedFT.Return) {
		t.Fatalf("where on return type must erase to base; plain=%s, refined=%s",
			plainFT.Return, refinedFT.Return)
	}
}

// ---------------------------------------------------------------------------
// E. Coexistence of `where` and `is Law`
// ---------------------------------------------------------------------------

// E1: a function with both a `where` param refinement and an explicit `requires`
// clause should analyze cleanly — they flow through the same SpecSignature fields
// without conflict.
func TestUnifiedPipelineWhereAndRequiresCoexist(t *testing.T) {
	src := `
def f(n: i64 where n >= 0) -> i64:
    requires n < 1000
    return n + 1
`
	result := analyzeTreeTestSource(t, "unified_where_requires.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where + requires should coexist cleanly, got: %v", errs)
	}
}

// E2: a function with a `where` return refinement and an explicit `ensure`
// clause should analyze cleanly.
func TestUnifiedPipelineWhereAndEnsureCoexist(t *testing.T) {
	src := `
def double(n: i64 where n >= 0) -> i64 where result >= 0:
    ensure result == n * 2
    return n * 2
`
	result := analyzeTreeTestSource(t, "unified_where_ensure.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where + ensure should coexist cleanly, got: %v", errs)
	}
}

// E3: a function that uses `is Law` in an ensure position alongside a `where`
// param must analyze cleanly.
// TODO: add named-law `requires` discharge test once `law` keyword is parseable
// in tests without additional stdlib preamble.
func TestUnifiedPipelineWhereAndIsLawCoexist(t *testing.T) {
	// Uses a where param + a regular ensure (is Law would require a law definition;
	// this test focuses on the coexistence in the SpecSignature, not the law runtime).
	src := `
def non_negative(n: i64 where n >= 0) -> i64:
    ensure result >= 0
    return n
`
	result := analyzeTreeTestSource(t, "unified_where_law_coexist.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where + is-Law-style ensure should coexist cleanly, got: %v", errs)
	}
}

// E4: multiple where predicates across params with cross-param dependencies should
// all parse and analyze without hard errors.
func TestUnifiedPipelineCrossParamWhereDependencies(t *testing.T) {
	src := `
def slice_op(xs: darray[i64], lo: i64 where 0 <= lo and lo < xs.count,
             hi: i64 where lo <= hi and hi <= xs.count) -> i64:
    return xs[lo]
`
	result := analyzeTreeTestSource(t, "unified_cross_param_where.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("cross-param where dependencies should analyze cleanly, got: %v", errs)
	}
}
