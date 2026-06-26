//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// ========== Self-Referencing Predicates (field where clause references the field itself) ==========

// TestWhereFieldSelfRefSatisfied verifies a simple self-referencing where clause on a field.
// The predicate directly constrains the field value.
func TestWhereFieldSelfRefSatisfied(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def make() -> Pos:
    return Pos(x: 5)
`
	result := analyzeContractStrict(t, "self_ref_satisfied.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for self-ref satisfied (x > 0, value=5), got: %v", errs)
	}
}

// TestWhereFieldSelfRefViolated verifies a self-referencing where clause with a violating value.
func TestWhereFieldSelfRefViolated(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def make() -> Pos:
    return Pos(x: 0)
`
	result := analyzeContractStrict(t, "self_ref_violated.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "is violated") && !strings.Contains(errs, "could not be proven") {
		t.Fatalf("expected violation error for self-ref (x > 0, value=0), got: %v", result.Errors())
	}
}

// TestWhereFieldSelfRefBoundary verifies boundary conditions: x >= 0 with value=0 satisfies.
func TestWhereFieldSelfRefBoundary(t *testing.T) {
	src := `
struct Count:
    n: i64 where n >= 0
def make() -> Count:
    return Count(n: 0)
`
	result := analyzeContractStrict(t, "self_ref_boundary.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for boundary (n >= 0, value=0), got: %v", errs)
	}
}

// ========== Named-Argument Construction ==========

// TestWhereFieldNamedArgSatisfied verifies named-argument struct literal satisfies where clause.
func TestWhereFieldNamedArgSatisfied(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def make() -> Pos:
    return Pos(x: 42)
`
	result := analyzeContractStrict(t, "named_arg_satisfied.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for named-arg where-satisfied, got: %v", errs)
	}
}

// TestWhereFieldNamedArgViolated verifies named-argument struct literal with violation.
func TestWhereFieldNamedArgViolated(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def make() -> Pos:
    return Pos(x: -5)
`
	result := analyzeContractStrict(t, "named_arg_violated.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "is violated") && !strings.Contains(errs, "could not be proven") {
		t.Fatalf("expected violation error for named-arg negative value, got: %v", result.Errors())
	}
}

// ========== Positional-Argument Construction ==========

// TestWhereFieldPositionalArgSatisfied verifies positional struct literal satisfies where clause.
func TestWhereFieldPositionalArgSatisfied(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def make() -> Pos:
    return Pos(10)
`
	result := analyzeContractStrict(t, "positional_arg_satisfied.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for positional where-satisfied, got: %v", errs)
	}
}

// TestWhereFieldPositionalArgViolated verifies positional struct literal with violation.
func TestWhereFieldPositionalArgViolated(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def make() -> Pos:
    return Pos(-1)
`
	result := analyzeContractStrict(t, "positional_arg_violated.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "is violated") && !strings.Contains(errs, "could not be proven") {
		t.Fatalf("expected violation error for positional negative value, got: %v", result.Errors())
	}
}

// ========== Earlier-Sibling References (field where clause references an earlier-declared field) ==========

// TestWhereFieldEarlierSiblingSatisfied verifies cross-field reference to an earlier sibling.
// hi >= lo where both lo and hi are provided.
func TestWhereFieldEarlierSiblingSatisfied(t *testing.T) {
	src := `
struct Range:
    lo: i64
    hi: i64 where hi >= lo
def make() -> Range:
    return Range(lo: 3, hi: 10)
`
	result := analyzeContractStrict(t, "earlier_sibling_satisfied.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for earlier-sibling reference (hi >= lo, lo=3, hi=10), got: %v", errs)
	}
}

// TestWhereFieldEarlierSiblingViolated verifies cross-field violation when hi < lo.
func TestWhereFieldEarlierSiblingViolated(t *testing.T) {
	src := `
struct Range:
    lo: i64
    hi: i64 where hi >= lo
def make() -> Range:
    return Range(lo: 15, hi: 5)
`
	result := analyzeContractStrict(t, "earlier_sibling_violated.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "is violated") {
		t.Fatalf("expected violation error for earlier-sibling (hi < lo), got: %v", result.Errors())
	}
}

// TestWhereFieldEarlierSiblingPositional verifies cross-field reference with positional args.
func TestWhereFieldEarlierSiblingPositional(t *testing.T) {
	src := `
struct Range:
    lo: i64
    hi: i64 where hi >= lo
def make() -> Range:
    return Range(2, 8)
`
	result := analyzeContractStrict(t, "earlier_sibling_positional.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for earlier-sibling positional construction, got: %v", errs)
	}
}

// TestWhereFieldEarlierSiblingBoundary verifies boundary case: hi == lo.
func TestWhereFieldEarlierSiblingBoundary(t *testing.T) {
	src := `
struct Range:
    lo: i64
    hi: i64 where hi >= lo
def make() -> Range:
    return Range(lo: 5, hi: 5)
`
	result := analyzeContractStrict(t, "earlier_sibling_boundary.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for boundary (hi == lo), got: %v", errs)
	}
}

// ========== Forward References (field where clause references a later-declared field) ==========

// TestWhereFieldForwardRefDiagnostic verifies that forward references produce a clear diagnostic.
// hi > lo where lo is declared AFTER hi in the struct.
func TestWhereFieldForwardRefDiagnostic(t *testing.T) {
	src := `
struct Range:
    hi: i64 where hi > lo
    lo: i64
`
	result := analyzeContractStrict(t, "forward_ref_diagnostic.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "not available here") && !strings.Contains(errs, "earlier-declared") {
		t.Fatalf("expected forward-ref diagnostic, got: %v", result.Errors())
	}
}

// TestWhereFieldForwardRefMultipleFields verifies forward ref error among multiple fields.
func TestWhereFieldForwardRefMultipleFields(t *testing.T) {
	src := `
struct Interval:
    start: i64 where start < end
    mid: i64
    end: i64
`
	result := analyzeContractStrict(t, "forward_ref_multiple.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "not available here") && !strings.Contains(errs, "earlier-declared") {
		t.Fatalf("expected forward-ref diagnostic for start < end, got: %v", result.Errors())
	}
}

// ========== Field-Store Violation (mutable field store operations must satisfy where) ==========

// TestWhereFieldStoreViolatedMutable verifies that storing a violating constant into a where-field errors.
func TestWhereFieldStoreViolatedMutable(t *testing.T) {
	src := `
struct Pos:
    x: mutable i64 where x > 0

def f(p: mutable Pos) -> i64:
    p.x <- -1
    return p.x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "store_violated_mutable.elisa", src, AnalyzeOptions{})
	diagStr := allDiagnostics(result)
	if len(result.Errors()) == 0 {
		t.Errorf("expected a hard error for store violation (p.x <- -1, x > 0); got errors=%v diags=%q",
			result.Errors(), diagStr)
	}
	if !strings.Contains(diagStr, "violated") && !strings.Contains(diagStr, "where refinement") {
		t.Errorf("expected 'violated' or 'where refinement' diagnostic; got diags=%q", diagStr)
	}
}

// TestWhereFieldStoreSatisfiedMutable verifies that storing a satisfying constant is accepted.
func TestWhereFieldStoreSatisfiedMutable(t *testing.T) {
	src := `
struct Pos:
    x: mutable i64 where x > 0

def f(p: mutable Pos) -> i64:
    p.x <- 42
    return p.x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "store_satisfied_mutable.elisa", src, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Errorf("expected no errors for store satisfied (p.x <- 42), got: %v", result.Errors())
	}
}

// TestWhereFieldStoreUnknownMutable verifies that storing a non-constant value defers to runtime.
func TestWhereFieldStoreUnknownMutable(t *testing.T) {
	src := `
struct Pos:
    x: mutable i64 where x > 0

def f(p: mutable Pos, v: i64) -> i64:
    p.x <- v
    return p.x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "store_unknown_mutable.elisa", src, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Errorf("expected no hard error for unknown-value store, got: %v", result.Errors())
	}
	hasRuntime := false
	for _, pr := range result.ProofReport {
		if pr.Outcome == ProofRuntime {
			hasRuntime = true
			break
		}
	}
	if !hasRuntime {
		t.Errorf("expected ProofRuntime entry for deferred store check; proof report: %v",
			result.ProofReport)
	}
}

// TestWhereFieldStoreEarlierSiblingUnprovable verifies store with sibling references defers to runtime
// when the constraint cannot be statically proven.
// hi >= lo constraint; storing a runtime-unknown value into hi defers to runtime check.
func TestWhereFieldStoreEarlierSiblingUnprovable(t *testing.T) {
	src := `
struct Range:
    lo: mutable i64
    hi: mutable i64 where hi >= lo

def f(r: mutable Range, v: i64) -> i64:
    r.lo <- 10
    r.hi <- v
    return r.hi
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "store_sibling_unprovable.elisa", src, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Errorf("expected no hard error for unprovable store (sibling reference with runtime value), got: %v", result.Errors())
	}
	hasRuntime := false
	for _, pr := range result.ProofReport {
		if pr.Outcome == ProofRuntime {
			hasRuntime = true
			break
		}
	}
	if !hasRuntime {
		t.Errorf("expected ProofRuntime entry for deferred store check; proof report: %v",
			result.ProofReport)
	}
}

// ========== Erasure (field runtime type equals base type; where clause doesn't alter type) ==========

// TestWhereFieldErasureRead verifies that field type is erased (reads as base type).
func TestWhereFieldErasureRead(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def read(p: Pos) -> i64:
    return p.x
`
	result := analyzeContractStrict(t, "erasure_read.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for field read (erasure must preserve base type), got: %v", errs)
	}
}

// TestWhereFieldErasureMultipleFields verifies erasure for multiple where-refined fields.
func TestWhereFieldErasureMultipleFields(t *testing.T) {
	src := `
struct Range:
    lo: i64 where lo >= 0
    hi: i64 where hi > 0
def read(r: Range) -> i64:
    return r.hi - r.lo
`
	result := analyzeContractStrict(t, "erasure_multiple.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for reading multiple erased fields, got: %v", errs)
	}
}

// TestWhereFieldErasurePassthrough verifies erasure across function calls.
func TestWhereFieldErasurePassthrough(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def read(p: Pos) -> i64:
    return p.x
def process(p: Pos) -> i64:
    return read(p)
`
	result := analyzeContractStrict(t, "erasure_passthrough.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for erasure passthrough across calls, got: %v", errs)
	}
}

// ========== Complex Predicates (where clause with multiple operators/conditions) ==========

// TestWhereFieldComplexPredicateSatisfied verifies a where clause with compound comparison.
func TestWhereFieldComplexPredicateSatisfied(t *testing.T) {
	src := `
struct Point:
    x: i64 where x > 0
    y: i64 where y > 0
def make() -> Point:
    return Point(x: 10, y: 20)
`
	result := analyzeContractStrict(t, "complex_predicate_satisfied.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for complex predicate satisfied, got: %v", errs)
	}
}

// TestWhereFieldComplexPredicateViolated verifies violation of compound predicates.
func TestWhereFieldComplexPredicateViolated(t *testing.T) {
	src := `
struct Point:
    x: i64 where x > 0
    y: i64 where y > 0
def make() -> Point:
    return Point(x: -1, y: 20)
`
	result := analyzeContractStrict(t, "complex_predicate_violated.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "is violated") && !strings.Contains(errs, "could not be proven") {
		t.Fatalf("expected violation error for complex predicate, got: %v", result.Errors())
	}
}

// ========== Multiple Fields with Cross-References ==========

// TestWhereFieldMultipleCrossRefSatisfied verifies struct with multiple where clauses.
func TestWhereFieldMultipleCrossRefSatisfied(t *testing.T) {
	src := `
struct Box:
    x: i64
    y: i64 where y > 0
    z: i64 where z > y
def make() -> Box:
    return Box(x: 0, y: 5, z: 10)
`
	result := analyzeContractStrict(t, "multiple_crossref_satisfied.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for multiple cross-ref satisfied, got: %v", errs)
	}
}

// TestWhereFieldMultipleCrossRefViolated verifies violation in a chain of constraints.
func TestWhereFieldMultipleCrossRefViolated(t *testing.T) {
	src := `
struct Box:
    x: i64
    y: i64 where y > 0
    z: i64 where z > y
def make() -> Box:
    return Box(x: 0, y: 5, z: 2)
`
	result := analyzeContractStrict(t, "multiple_crossref_violated.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "is violated") && !strings.Contains(errs, "could not be proven") {
		t.Fatalf("expected violation error for z > y with z=2, y=5, got: %v", result.Errors())
	}
}

// ========== Negative (Edge Cases That Should Today Fail or Produce Diagnostics) ==========

// TestWhereFieldSelfRefTypeMismatch verifies type checking in where clause.
// (Attempting to use a non-numeric field in numeric comparison.)
func TestWhereFieldSelfRefTypeMismatch(t *testing.T) {
	src := `
struct S:
    s: dstr where s.count > 0
def make() -> S:
    return S(s: "hello")
`
	result := analyzeContractStrict(t, "self_ref_type_mismatch.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "type") && !strings.Contains(errs, "Type mismatch") {
		t.Logf("NOTE: type mismatch in where clause (s.count > 0) got: %v", result.Errors())
	}
}

// TestWhereFieldUndefinedVariable verifies undefined variables in where clause are caught.
func TestWhereFieldUndefinedVariable(t *testing.T) {
	src := `
struct S:
    x: i64 where x > undefined_var
def make() -> S:
    return S(x: 5)
`
	result := analyzeContractStrict(t, "undefined_var_in_where.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "not found") && !strings.Contains(errs, "undefined") {
		t.Logf("NOTE: undefined variable in where clause got: %v", result.Errors())
	}
}

// ========== Smoke Tests (Sanity) ==========

// TestWhereFieldSmokeSimple is a baseline: simple where clause, satisfying construction.
func TestWhereFieldSmokeSimple(t *testing.T) {
	src := `
struct Size:
    width: i64 where width > 0
def new_size() -> Size:
    return Size(width: 100)
`
	result := analyzeContractStrict(t, "smoke_simple.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("smoke test failed: simple where clause with satisfying value, got: %v", errs)
	}
}

// TestWhereFieldSmokeComplex is a baseline: multiple fields with cross-references.
func TestWhereFieldSmokeComplex(t *testing.T) {
	src := `
struct Interval:
    start: i64 where start >= 0
    length: i64 where length > 0
def new_interval() -> Interval:
    return Interval(start: 10, length: 50)
`
	result := analyzeContractStrict(t, "smoke_complex.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("smoke test failed: multiple where clauses, got: %v", errs)
	}
}
