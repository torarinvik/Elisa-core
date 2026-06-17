//go:build cgo

package semantic

import "testing"

// A refinement type erases to its base: `n: i64 is Positive` is an i64 inside the function
// (docs/85 Stage 1c-1 — parse + represent erased + validate the predicate).
func TestRefinementTypeErasesToBase(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def f(n: i64 is Positive) -> i64:
    return n + 1

def g() -> i64:
    x: i64 is Positive = 5
    return x
`
	result := analyzeTreeTestSource(t, "refine_base.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("refinement type should erase to its base and analyze cleanly, got: %v", errs)
	}
}

// A refinement predicate that is not a law is an error.
func TestRefinementTypeNonLawRejected(t *testing.T) {
	src := `
def f(n: i64 is Bogus) -> i64:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_nonlaw.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is not a law") {
		t.Fatalf("non-law refinement predicate should be rejected, got:\n%s", allDiagnostics(result))
	}
}

// A refinement whose law subject type does not accept the base type is an error.
func TestRefinementTypeSubjectMismatchRejected(t *testing.T) {
	src := `
law NonEmpty(self: darray[i64]&) = self.count > 0

def f(n: i64 is NonEmpty) -> i64:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_subjmismatch.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "expects a subject of type") {
		t.Fatalf("subject-type mismatch should be rejected, got:\n%s", allDiagnostics(result))
	}
}

// A generic law accepts any matching base via inference.
func TestRefinementTypeGenericLawAccepts(t *testing.T) {
	src := `
law NonEmpty[T](self: darray[T]&) = self.count > 0

def f(xs: darray[f64] is NonEmpty) -> usize:
    return xs.count
`
	result := analyzeTreeTestSource(t, "refine_generic.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("generic-law refinement should analyze, got: %v", errs)
	}
}

// Tier-1 constant discharge: a constant that satisfies the refinement is PROVEN at compile time —
// no runtime check is recorded (docs/85 Stage 1d).
func TestRefinementConstantProvenElided(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def f() -> i64:
    x: i64 is Nat = 5
    return x
`
	result := analyzeTreeTestSource(t, "refine_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("constant 5 satisfies Nat, should be clean, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("proven refinement should emit NO runtime check, got %d", len(result.RefinementChecks))
	}
}

// A constant that violates the refinement is REFUTED at compile time — a hard error, not a
// runtime trap.
func TestRefinementConstantRefutedErrors(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def f() -> i64:
    x: i64 is Nat = 0 - 3
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_refuted.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("constant -3 violates Nat: expected a compile-time refutation, got:\n%s", allDiagnostics(result))
	}
}

// A non-constant value falls back to a runtime boundary check.
func TestRefinementNonConstantRuntimeCheck(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def f(n: i64) -> i64:
    x: i64 is Nat = n
    return x
`
	result := analyzeTreeTestSource(t, "refine_runtime.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("non-const refinement should analyze (runtime fallback), got: %v", errs)
	}
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("non-const refinement should record a runtime check")
	}
}

// The user must KNOW when a refinement isn't statically guaranteed: a non-provable refinement
// WARNS by default (not an error) — visible, but prototyping stays fluid (docs/85).
func TestRefinementUnprovenWarnsByDefault(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def f(n: i64) -> i64:
    x: i64 is Nat = n
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_warn.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("unprovable refinement should be reported, got:\n%s", allDiagnostics(result))
	}
	if len(result.Errors()) != 0 {
		t.Fatalf("by default the proof fallback is a WARNING, not an error, got: %v", result.Errors())
	}
}

// Under -strict it is a hard error — the Dafny-like prove-it-or-fail mode.
func TestRefinementUnprovenStrictErrors(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def f(n: i64) -> i64:
    x: i64 is Nat = n
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_strict.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) == 0 {
		t.Fatal("under -strict an unprovable refinement must be a hard error")
	}
}

// A statically-proven refinement emits NO proof diagnostic — silence means a real guarantee.
func TestRefinementProvenNoProofDiagnostic(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def f() -> i64:
    x: i64 is Nat = 5
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_silent.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("a proven refinement must NOT warn/error even under -strict, got: %v", result.Errors())
	}
}
