//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// TestWhereStructFieldSiblingCleanNamed verifies that a named-arg struct literal satisfies a
// cross-field where predicate referencing an earlier field (e.g. hi >= lo).
func TestWhereStructFieldSiblingCleanNamed(t *testing.T) {
	src := `
struct Range:
    lo: i64
    hi: i64 where hi >= lo
def make() -> Range:
    return Range{lo: 3, hi: 10}
`
	result := analyzeContractStrict(t, "where_sibling_named_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for Range(lo:3, hi:10), got: %v", errs)
	}
}

// TestWhereStructFieldSiblingViolatedNamed verifies that a named-arg struct literal with a
// constant value that violates the cross-field where predicate is rejected.
func TestWhereStructFieldSiblingViolatedNamed(t *testing.T) {
	src := `
struct Range:
    lo: i64
    hi: i64 where hi >= lo
def make() -> Range:
    return Range{lo: 10, hi: 3}
`
	result := analyzeContractStrict(t, "where_sibling_named_bad.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "is violated") {
		t.Fatalf("expected violation error for Range(lo:10, hi:3), got: %v", result.Errors())
	}
}

// TestWhereStructFieldSiblingCleanPositional verifies that positional construction also satisfies
// a cross-field where predicate referencing an earlier field.
func TestWhereStructFieldSiblingCleanPositional(t *testing.T) {
	src := `
struct Range:
    lo: i64
    hi: i64 where hi >= lo
def make() -> Range:
    return Range{lo: 2, hi: 5}
`
	result := analyzeContractStrict(t, "where_sibling_pos_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for Range(2, 5), got: %v", errs)
	}
}

// TestWhereStructFieldSiblingViolatedPositional verifies that positional construction with a
// constant violation of the cross-field where predicate is rejected.
func TestWhereStructFieldSiblingViolatedPositional(t *testing.T) {
	src := `
struct Range:
    lo: i64
    hi: i64 where hi >= lo
def make() -> Range:
    return Range{lo: 5, hi: 2}
`
	result := analyzeContractStrict(t, "where_sibling_pos_bad.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "is violated") {
		t.Fatalf("expected violation error for Range(5, 2), got: %v", result.Errors())
	}
}

// TestWhereStructFieldForwardRefError verifies that a field where predicate referencing a
// LATER-declared sibling produces a diagnostic (forward references are not allowed).
func TestWhereStructFieldForwardRefError(t *testing.T) {
	src := `
struct Range:
    hi: i64 where hi >= lo
    lo: i64
`
	result := analyzeContractStrict(t, "where_sibling_fwdref.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "not available here") && !strings.Contains(errs, "earlier-declared") {
		t.Fatalf("expected forward-reference diagnostic, got: %v", result.Errors())
	}
}

// TestWhereStructFieldSiblingErasure verifies that sibling where predicates do not alter the
// field's runtime type — both lo and hi are still plain i64.
func TestWhereStructFieldSiblingErasure(t *testing.T) {
	src := `
struct Range:
    lo: i64
    hi: i64 where hi >= lo
def read(r: Range) -> i64:
    return r.hi - r.lo
`
	result := analyzeContractStrict(t, "where_sibling_erasure.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for field read after sibling where (erasure), got: %v", errs)
	}
}
