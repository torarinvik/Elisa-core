//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// TestWhereStructFieldClean verifies that a struct literal with a satisfying where-predicate field
// value is accepted without errors.
func TestWhereStructFieldClean(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def make() -> Pos:
    return Pos(x: 5)
`
	result := analyzeContractStrict(t, "where_field_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for satisfying field value, got: %v", errs)
	}
}

// TestWhereStructFieldViolated verifies that a struct literal with a constant value that violates
// the field's where predicate is rejected.
func TestWhereStructFieldViolated(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def make() -> Pos:
    return Pos(x: -1)
`
	result := analyzeContractStrict(t, "where_field_bad.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "is violated") && !strings.Contains(errs, "could not be proven") {
		t.Fatalf("expected violation error for Pos(x: -1), got: %v", result.Errors())
	}
}

// TestWhereStructFieldErasure verifies that the field's runtime type is the base type (erasure
// preserved): the field resolves as i64, not a where-refined type.
func TestWhereStructFieldErasure(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def read(p: Pos) -> i64:
    return p.x
`
	result := analyzeContractStrict(t, "where_field_erasure.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for field read (erasure must preserve base type), got: %v", errs)
	}
}

// TestWhereStructFieldForwardRefDiagnostic verifies that a forward reference (field where predicate
// references a LATER-declared field) produces a clear diagnostic.
func TestWhereStructFieldForwardRefDiagnostic(t *testing.T) {
	src := `
struct Range:
    hi: i64 where hi > lo
    lo: i64
`
	result := analyzeContractStrict(t, "where_field_fwdref.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "not available here") && !strings.Contains(errs, "earlier-declared") {
		t.Fatalf("expected forward-ref diagnostic for hi > lo (lo is later), got: %v", result.Errors())
	}
}

// TestWhereStructFieldPositionalConstruct verifies positional (non-named-arg) struct literals also
// discharge the where predicate.
func TestWhereStructFieldPositionalClean(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def make() -> Pos:
    return Pos(10)
`
	result := analyzeContractStrict(t, "where_field_pos_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no errors for Pos(10), got: %v", errs)
	}
}

func TestWhereStructFieldPositionalViolated(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def make() -> Pos:
    return Pos(0)
`
	result := analyzeContractStrict(t, "where_field_pos_bad.elisa", src)
	errs := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errs, "is violated") && !strings.Contains(errs, "could not be proven") {
		t.Fatalf("expected violation error for Pos(0), got: %v", result.Errors())
	}
}
