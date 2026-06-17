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

// FLOW entailment (docs/85 1d-2): inside `if a > 5:`, `a` satisfies `Nat` (a >= 0) by the branch
// condition — proven statically, no warning, no runtime check. The headline refinement-type case.
func TestRefinementFlowEntailmentProven(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def f(a: i64) -> i64:
    if a > 5:
        x: i64 is Nat = a
        return x
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_flow.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`if a > 5` should statically prove `a is Nat` (no error even under -strict), got: %v", result.Errors())
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("flow-proven refinement should emit NO runtime check, got %d", len(result.RefinementChecks))
	}
}

// Without the guard, the same refinement is NOT provable → warning (visible) + runtime check.
func TestRefinementFlowNoGuardWarns(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def f(a: i64) -> i64:
    x: i64 is Nat = a
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_noguard.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("unguarded refinement should warn, got:\n%s", allDiagnostics(result))
	}
}

// A guard that does NOT entail the predicate stays unproven (sound): `a > -5` does not imply a >= 0.
func TestRefinementFlowInsufficientGuardWarns(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def f(a: i64) -> i64:
    if a > 0 - 5:
        x: i64 is Nat = a
        return x
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_weakguard.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("`a > -5` does NOT entail Nat; must stay unproven, got:\n%s", allDiagnostics(result))
	}
}

const boundedLaw = `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
`

// Parametric refinement, constant: 42 ∈ [0,500] proven; bracket args bound into the law body.
func TestRefinementParametricConstantProven(t *testing.T) {
	src := boundedLaw + `
def f() -> i64:
    x: i64 is Bounded[0, 500] = 42
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_param_const.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("42 ∈ [0,500] should prove (clean under -strict), got: %v", result.Errors())
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("proven parametric refinement should emit no runtime check")
	}
}

// Range sugar `[0..500]` is the two endpoints; a violating constant is refuted.
func TestRefinementParametricRangeRefuted(t *testing.T) {
	src := boundedLaw + `
def f() -> i64:
    x: i64 is Bounded[0..500] = 600
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_param_refute.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("600 ∉ [0,500] should be refuted, got:\n%s", allDiagnostics(result))
	}
}

// Flow + parametric: a ∈ [10,20] ⊆ [0,500] proven from the branch condition.
func TestRefinementParametricFlowProven(t *testing.T) {
	src := boundedLaw + `
def f(a: i64) -> i64:
    if a >= 10 and a <= 20:
        x: i64 is Bounded[0..500] = a
        return x
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_param_flow.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("a ∈ [10,20] ⊆ [0,500] should prove, got: %v", result.Errors())
	}
}

// Flow + parametric, insufficient: a ∈ [10,∞) does NOT prove the upper bound — stays unproven.
func TestRefinementParametricFlowInsufficient(t *testing.T) {
	src := boundedLaw + `
def f(a: i64) -> i64:
    if a >= 10:
        x: i64 is Bounded[0..500] = a
        return x
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_param_flow_weak.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("a >= 10 alone does not bound the top of [0,500]; must stay unproven, got:\n%s", allDiagnostics(result))
	}
}
