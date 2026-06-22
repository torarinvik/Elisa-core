//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// POSITIVE: a `ghost` struct field is accepted and usable in a struct invariant that relates the
// concrete representation to the abstract model. The ghost field carries verification-only model
// state; it must be recorded on the struct type and stripped from the concrete (codegen) field list.
func TestGhostFieldUsableInStructInvariant(t *testing.T) {
	src := `
struct Box:
    concrete: i64
    ghost model: i64
    invariant self.concrete == self.model

def use(b: Box&) -> i64:
    return b.concrete
`
	result := analyzeWithSMT(t, "ghost_field_invariant.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis of a ghost field used in a struct invariant, got: %v", errs)
	}
}

// POSITIVE: a method `ensure result == self.<ghost>` (a refinement of the concrete return value onto
// the abstract ghost model) discharges under -strict, because the struct invariant relating concrete
// to model is assumed as a method-entry fact.
func TestGhostFieldRefinementHoldsDischarges(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64
    invariant self.concrete == self.model

def get_model(self: Counter&) -> i64:
    ensure result == self.model
    return self.concrete
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ghost_field_refine_ok.elisa", src, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a concrete-vs-ghost refinement that holds to discharge cleanly, got: %v", errs)
	}
}

// SOUNDNESS: a concrete-vs-ghost refinement that does NOT hold must be reported under -strict (no
// vacuous pass). Here the ensure claims the result is the model PLUS ONE, which the invariant refutes.
func TestGhostFieldRefinementFailsRejected(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64
    invariant self.concrete == self.model

def get_model(self: Counter&) -> i64:
    ensure result == self.model + 1
    return self.concrete
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ghost_field_refine_bad.elisa", src, AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true})
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven") {
		t.Fatalf("expected a false concrete-vs-ghost refinement to be rejected under -strict, got: %v", result.Errors())
	}
}

// SOUNDNESS: reading a ghost field in ordinary (non-contract) runtime code is rejected — the ghost is
// erased from codegen, so real code may never read it (invariant: no ghost-to-real flow).
func TestGhostFieldReadInRealCodeRejected(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64

def leak(self: Counter&) -> i64:
    return self.model
`
	result := analyzeWithSMT(t, "ghost_field_real_read.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "ghost field") {
		t.Fatalf("expected a ghost-field read in real code to be rejected, got: %v", result.Errors())
	}
}

// A ghost field is verification-only and carries no runtime initializer; a default value is rejected.
func TestGhostFieldDefaultValueRejected(t *testing.T) {
	src := `
struct Counter:
    concrete: i64
    ghost model: i64 = 0
`
	result := analyzeWithSMT(t, "ghost_field_default.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "ghost field") {
		t.Fatalf("expected a ghost field with a default value to be rejected, got: %v", result.Errors())
	}
}

// A field literally named `ghost` (not the keyword modifier) must still parse as an ordinary field:
// `ghost:` (name then colon) is a real field, distinguishable from `ghost name:` (modifier + name).
func TestFieldNamedGhostStillWorks(t *testing.T) {
	src := `
struct Spooky:
    ghost: i64

def read(s: Spooky&) -> i64:
    return s.ghost
`
	result := analyzeWithSMT(t, "field_named_ghost.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a field literally named `ghost` must keep working, got: %v", errs)
	}
}
