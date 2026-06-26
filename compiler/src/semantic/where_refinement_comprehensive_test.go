//go:build cgo

package semantic

import "testing"

// ---------------------------------------------------------------------------
// Param position
// ---------------------------------------------------------------------------

// A param with a valid `where` predicate that is a simple bool constant must analyze cleanly.
func TestWhereRefinementParamBoolConstant(t *testing.T) {
	src := `
def f(n: i64 where true) -> i64:
    return n
`
	result := analyzeTreeTestSource(t, "where_param_bool_const.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("param where true should analyze cleanly, got: %v", errs)
	}
}

// A param with a `where` that references the param itself (identifier) should
// currently be accepted at parse/analysis without a hard error (the predicate is
// deferred; discharge is the job of the proof engine, not type-checking).
// TODO: once the discharge path fully wires `where` predicates into the requires
// machinery, this test should also verify no proofLint is emitted for trivially
// provable predicates.
func TestWhereRefinementParamWithIdentifierReference(t *testing.T) {
	src := `
def get(xs: darray[i64], i: i64 where 0 <= i and i < xs.count) -> i64:
    return xs[i]
`
	result := analyzeTreeTestSource(t, "where_param_ident.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("param where with identifier reference should analyze cleanly (predicate is deferred), got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// Return position
// ---------------------------------------------------------------------------

// A return `where` predicate (using `result`) should analyze cleanly.
func TestWhereRefinementReturnWithResultIdent(t *testing.T) {
	src := `
def pick(n: i64, k: i64 where 0 <= k and k < n) -> i64 where 0 <= result and result < n:
    return k
`
	result := analyzeTreeTestSource(t, "where_return_result.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("return where with result ident should analyze cleanly, got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// Local variable position
// ---------------------------------------------------------------------------

// A local `where` predicate should analyze cleanly.
func TestWhereRefinementLocalVar(t *testing.T) {
	src := `
def safe(xs: darray[i64]) -> i64:
    i: i64 where 0 <= i and i < xs.count = 0
    return xs[i]
`
	result := analyzeTreeTestSource(t, "where_local.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("local where should analyze cleanly, got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// Bool-type enforcement
// ---------------------------------------------------------------------------

// A non-bool constant predicate (integer literal) must be rejected.
func TestWhereRefinementNonBoolConstantRejected(t *testing.T) {
	src := `
def f(n: i64 where 1) -> i64:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_nonbool_const.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "where refinement predicate must be bool") {
		t.Fatalf("non-bool where predicate should be rejected, got:\n%s", allDiagnostics(result))
	}
}

// The improved diagnostic should also mention the subject type.
func TestWhereRefinementNonBoolDiagnosticMentionsSubjectType(t *testing.T) {
	src := `
def f(n: i64 where 1) -> i64:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_nonbool_subject.elisa", src, AnalyzeOptions{})
	diag := allDiagnostics(result)
	if !contains(diag, "subject type") {
		t.Fatalf("improved diagnostic should mention subject type, got:\n%s", diag)
	}
}

// ---------------------------------------------------------------------------
// Representation erasure: SameType / AssignableTo guardrail
// ---------------------------------------------------------------------------

// A value of type `i64` must be assignable to a binding typed `i64 where p`.
// `where` does NOT create a runtime-distinct type; it only adds a proof obligation.
// TODO: once the discharge/proof path is fully wired, this test should also verify
// that the proof obligation (proofLint) is emitted when assigning an unconstrained value.
func TestWhereRefinementErasesToBaseForAssignability(t *testing.T) {
	src := `
def f(n: i64) -> i64:
    x: i64 where x > 0 = n
    return x
`
	// No hard type error: the `where` predicate is representation-erased.
	// (A proofLint warning is acceptable; a type mismatch error is not.)
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_assignability.elisa", src, AnalyzeOptions{})
	for _, e := range result.Errors() {
		if contains(e, "type mismatch") || contains(e, "cannot assign") {
			t.Fatalf("where should not create a distinct runtime type, got hard type error: %v", e)
		}
	}
}

// A function accepting `i64 where p` must accept a plain `i64` argument without
// a type-mismatch error (proof obligation may be emitted as a proofLint).
func TestWhereRefinementParamAcceptsPlainBaseType(t *testing.T) {
	src := `
def constrained(n: i64 where n > 0) -> i64:
    return n

def caller(x: i64) -> i64:
    return constrained(x)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "where_param_plain_arg.elisa", src, AnalyzeOptions{})
	for _, e := range result.Errors() {
		if contains(e, "type mismatch") || contains(e, "cannot assign") {
			t.Fatalf("where should not create a distinct runtime type at call sites, got hard type error: %v", e)
		}
	}
}

// ---------------------------------------------------------------------------
// Relationship to named-law refinements: co-existence
// ---------------------------------------------------------------------------

// A `where` refinement and a named law refinement on separate params in the same
// function must both parse and analyze correctly.
func TestWhereRefinementCoexistsWithNamedLaw(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def f(a: i64 is Positive, b: i64 where b > 0) -> i64:
    return a + b
`
	result := analyzeTreeTestSource(t, "where_and_named_law.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where and named-law refinements should coexist cleanly, got: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// Multiple params: earlier param reference in where predicate
// ---------------------------------------------------------------------------

// A param `where` that references an earlier param by name should analyze cleanly;
// the predicate is a dependent refinement that the proof engine will discharge.
func TestWhereRefinementParamReferencesEarlierParam(t *testing.T) {
	src := `
def bounded_sum(lo: i64, hi: i64 where lo <= hi) -> i64:
    return lo + hi
`
	result := analyzeTreeTestSource(t, "where_earlier_param.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("where referencing earlier param should analyze cleanly, got: %v", errs)
	}
}
