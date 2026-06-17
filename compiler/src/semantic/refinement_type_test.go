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

// `if x is Law:` narrows x inside the branch: a refinement obligation on x discharges statically
// there with no runtime check (docs/85 — predicate-test narrowing).
func TestLawIsNarrowsBranch(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0
law Nat(self: i64) = self >= 0

def needs_nat(x: i64 is Nat) -> i64:
    return x

def f(n: i64) -> i64:
    if n is Positive:
        return needs_nat(n)
    return 0
`
	result := analyzeTreeTestSource(t, "law_is_narrow.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`if n is Positive` should narrow n so `needs_nat(n)` proves, got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("narrowed call arg should be statically proven (no runtime check), got %d", len(result.CallArgRefinementChecks))
	}
}

// `if x is Bounded[0,500]:` (parametric law) narrows x so a `Bounded[0,500]` obligation discharges.
func TestParametricLawIsNarrowsBranch(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def needs_b(x: i64 is Bounded[0, 500]) -> i64:
    return x

def f(n: i64) -> i64:
    if n is Bounded[0, 500]:
        return needs_b(n)
    return 0
`
	result := analyzeTreeTestSource(t, "param_law_narrow.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`if n is Bounded[0,500]` should narrow n so `needs_b(n)` proves, got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("narrowed parametric call arg should be statically proven, got %d", len(result.CallArgRefinementChecks))
	}
}

// A narrowing by a TIGHTER parametric range does not prove a WIDER obligation: `is Bounded[10,20]`
// must NOT discharge `Bounded[0,500]`'s... wait — tighter entails wider. The unsound direction is
// the reverse: narrowing by a wider range must not prove a tighter obligation.
func TestParametricLawIsNarrowingDoesNotOverprove(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def needs_tight(x: i64 is Bounded[10, 20]) -> i64:
    return x

def f(n: i64) -> i64:
    if n is Bounded[0, 500]:
        return needs_tight(n)
    return 0
`
	result := analyzeTreeTestSource(t, "param_law_overprove.elisa", src)
	if len(result.ReturnRefinementChecks)+len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("`is Bounded[0,500]` must NOT prove the tighter `Bounded[10,20]` — a runtime check is required")
	}
}

// Without the narrowing branch, the same call is unproven — confirms the narrowing is what proves it.
func TestLawIsNarrowingIsLoadBearing(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def needs_nat(x: i64 is Nat) -> i64:
    return x

def f(n: i64) -> i64:
    return needs_nat(n)
`
	result := analyzeTreeTestSource(t, "law_is_nonarrow.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("un-narrowed call arg should be unproven (runtime check recorded)")
	}
}

// The proof report records every discharge decision with its outcome (docs/85 --explain).
func TestProofReportRecordsOutcomes(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def needs_nat(x: i64 is Nat) -> i64:
    return x

def f(n: i64) -> i64:
    a: i64 is Nat = 5          # proven (const)
    b: i64 = needs_nat(n)      # runtime (n unknown)
    if n > 0:
        return needs_nat(n)    # proven (flow)
    return a + b
`
	result := analyzeTreeTestSource(t, "proof_report.elisa", src)
	var const_, flow, runtime int
	for _, f := range result.ProofReport {
		switch f.Outcome {
		case ProofProvenConst:
			const_++
		case ProofProvenFlow:
			flow++
		case ProofRuntime:
			runtime++
		}
	}
	if const_ == 0 || flow == 0 || runtime == 0 {
		t.Fatalf("proof report should record const/flow/runtime outcomes, got const=%d flow=%d runtime=%d (report=%+v)", const_, flow, runtime, result.ProofReport)
	}
}

// A refuted refinement is recorded as such in the report.
func TestProofReportRecordsRefutation(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def f() -> i64:
    x: i64 is Nat = 0 - 3
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "proof_refute.elisa", src, AnalyzeOptions{})
	found := false
	for _, f := range result.ProofReport {
		if f.Outcome == ProofRefuted {
			found = true
		}
	}
	if !found {
		t.Fatalf("a violated refinement should be recorded as refuted in the proof report, got %+v", result.ProofReport)
	}
}

// A refinement-typed RETURN proves statically for a satisfying constant — no runtime check.
func TestRefinementReturnConstantProven(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def f() -> i64 is Positive:
    return 5
`
	result := analyzeTreeTestSource(t, "refine_ret_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("returning 5 satisfies Positive, should be clean, got: %v", errs)
	}
	if len(result.ReturnRefinementChecks) != 0 {
		t.Fatalf("proven return refinement should emit NO runtime check, got %d", len(result.ReturnRefinementChecks))
	}
}

// A refinement-typed RETURN of a violating constant is REFUTED at compile time.
func TestRefinementReturnConstantRefuted(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def f() -> i64 is Positive:
    return 0 - 3
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_ret_refuted.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("returning -3 violates Positive: expected a compile-time refutation, got:\n%s", allDiagnostics(result))
	}
}

// A refinement-typed RETURN of a flow-proven value proves statically — no runtime check, clean.
func TestRefinementReturnFlowProven(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def f(n: i64) -> i64 is Positive:
    if n > 0:
        return n
    return 1
`
	result := analyzeTreeTestSource(t, "refine_ret_flow.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("flow-proven return should be clean, got: %v", errs)
	}
	if len(result.ReturnRefinementChecks) != 0 {
		t.Fatalf("flow-proven return refinement should emit NO runtime check, got %d", len(result.ReturnRefinementChecks))
	}
}

// A refinement-typed RETURN of an unproven side-effect-free value records a runtime check.
func TestRefinementReturnUnprovenRuntimeCheck(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def f(n: i64) -> i64 is Positive:
    return n
`
	result := analyzeTreeTestSource(t, "refine_ret_runtime.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unproven return should analyze (runtime fallback), got: %v", errs)
	}
	if len(result.ReturnRefinementChecks) == 0 {
		t.Fatalf("unproven return refinement should record a runtime check")
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

// Call-site discharge: a constant argument satisfying the param refinement is proven.
func TestRefinementCallArgConstantProven(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def needs_nat(x: i64 is Nat) -> i64:
    return x

def caller() -> i64:
    return needs_nat(7)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_callarg_ok.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("needs_nat(7) should prove (clean under -strict), got: %v", result.Errors())
	}
}

// Call-site discharge: a constant argument that violates the param refinement is refuted.
func TestRefinementCallArgRefuted(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def needs_nat(x: i64 is Nat) -> i64:
    return x

def caller() -> i64:
    return needs_nat(0 - 3)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_callarg_refute.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("needs_nat(-3) should be refuted, got:\n%s", allDiagnostics(result))
	}
}

// Call-site discharge: an unprovable argument warns by default, errors under -strict.
func TestRefinementCallArgUnprovenWarnsThenStrictErrors(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def needs_nat(x: i64 is Nat) -> i64:
    return x

def caller(n: i64) -> i64:
    return needs_nat(n)
`
	warn := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_callarg_warn.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(warn), "could not be proven statically") || len(warn.Errors()) != 0 {
		t.Fatalf("unprovable arg should warn (not error) by default, got:\n%s", allDiagnostics(warn))
	}
	strict := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_callarg_strict.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(strict.Errors()) == 0 {
		t.Fatal("unprovable call arg must be a hard error under -strict")
	}
}

// Call-site discharge composes with flow: needs_nat(a) inside `if a > 5` is proven.
func TestRefinementCallArgFlowProven(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def needs_nat(x: i64 is Nat) -> i64:
    return x

def caller(a: i64) -> i64:
    if a > 5:
        return needs_nat(a)
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "refine_callarg_flow.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("needs_nat(a) under `if a > 5` should prove, got: %v", result.Errors())
	}
}

// --- Mutable refinement flow (docs/85): predicate facts on mutable variables, invalidated at
// every mutation site. Today's range prover refuses mutables; these facts carry on mutables
// SOUNDLY because a mutation drops them. n is seeded from a parameter so it is never a tracked
// constant — the predicate fact is the ONLY thing that can discharge the obligation. ---

// A predicate fact gained by `if x is P:` discharges a later obligation on x even when x is a
// MUTABLE variable — sound because no mutation happens between the narrowing and the use.
func TestMutableRefinementFactProvesWhenUnmutated(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Positive:
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "mut_refine_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("narrowed mutable n (unmutated) should prove needs_pos(n), got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("unmutated narrowed mutable should be statically proven, got %d runtime checks", len(result.CallArgRefinementChecks))
	}
}

// SOUNDNESS CORE: a write to x between the narrowing and the use DROPS the predicate fact, so the
// obligation falls back to a runtime check. The compiler stops trusting the refinement exactly
// where the mutation happens.
func TestMutableRefinementFactDroppedByAssignment(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def f(seed: i64, other: i64) -> i64:
    n: mutable i64 = seed
    if n is Positive:
        n <- other
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "mut_refine_assign_drop.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("mutation `n = other` must drop the Positive fact, forcing a runtime check on needs_pos(n)")
	}
}

// A mutating call through a mutable ref (`bump(&n)`, the same shape as `arr.pop()`) also drops the
// fact — this is the "loses the refinement where it is called" behavior the design targets.
func TestMutableRefinementFactDroppedByRefCall(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def bump(x: mutable i64&) -> void:
    pass

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Positive:
        bump(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "mut_refine_refcall_drop.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("passing &n to a mutable-ref param must drop the Positive fact, forcing a runtime check")
	}
}

// --- Mutable refinement flow brick 2: `ensures <param> is Law` postconditions. The caller GAINS
// the fact at the call site (A); the callee's returns are checked (B) so the gain is backed. ---

// After calling a function that `ensures arr is Positive` on a by-ref param, the caller knows the
// predicate holds — a subsequent obligation on that variable proves with no runtime check, even
// though the ref call would otherwise DROP the fact (brick 1).
func TestEnsuresRefinementGainsFactAtCallSite(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def ensure_pos(p: mutable i64&) -> void ensures p is Positive:
    p <- 1

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    ensure_pos(&n)
    return needs_pos(n)
`
	result := analyzeTreeTestSource(t, "ensures_gain.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`ensures p is Positive` should let needs_pos(n) prove after ensure_pos(&n), got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("the gained postcondition fact should prove needs_pos(n) statically, got %d runtime checks", len(result.CallArgRefinementChecks))
	}
}

// Without the `ensures`, the same ref call DROPS the fact (brick 1) — confirms the postcondition is
// what carries the guarantee across the call.
func TestEnsuresRefinementGainIsLoadBearing(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def bump(p: mutable i64&) -> void:
    p <- 1

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Positive:
        bump(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "ensures_loadbearing.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("a ref call WITHOUT `ensures` must drop the fact, forcing a runtime check")
	}
}

// A refuted postcondition (`ensures p is Positive` but the body provably sets it non-positive) is a
// compile error from the static half.
func TestEnsuresRefinementRefutedStatically(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def bad(p: mutable i64&) -> void ensures p is Positive:
    p <- 0
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_refute.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("`p <- 0` violates the Positive postcondition: expected a compile-time refutation, got:\n%s", allDiagnostics(result))
	}
}

// `ensures` referencing an unknown parameter, or a non-law, is rejected.
func TestEnsuresRefinementValidation(t *testing.T) {
	bad := `
law Positive(self: i64) = self > 0
def f(p: mutable i64&) -> void ensures q is Positive:
    p <- 1
`
	r1 := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_unknown_param.elisa", bad, AnalyzeOptions{})
	if !contains(allDiagnostics(r1), "unknown parameter") {
		t.Fatalf("ensures on unknown param should be rejected, got:\n%s", allDiagnostics(r1))
	}
	notLaw := `
def f(p: mutable i64&) -> void ensures p is Nope:
    p <- 1
`
	r2 := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_notlaw.elisa", notLaw, AnalyzeOptions{})
	if !contains(allDiagnostics(r2), "is not a law") {
		t.Fatalf("ensures with a non-law predicate should be rejected, got:\n%s", allDiagnostics(r2))
	}
}

// Written-constant tracking through a ref pointee PROVES a postcondition: `p <- 1` makes the
// `ensures p is Positive` provable at the return (no warning), and the call-site gain follows.
func TestEnsuresRefinementProvenByWrittenConst(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def set_pos(p: mutable i64&) -> void ensures p is Positive:
    p <- 1
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_proven_write.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`p <- 1` should statically prove `ensures p is Positive` (clean under -strict), got: %v", result.Errors())
	}
	for _, f := range result.ProofReport {
		if f.Predicate == "Positive" && f.Outcome == ProofRuntime {
			t.Fatalf("postcondition should be proven (not runtime) after `p <- 1`, report=%+v", result.ProofReport)
		}
	}
}

// A non-constant write (`p <- v`) cannot be proven — reported as unproven (warning; -strict error).
func TestEnsuresRefinementUnprovenNonConstWrite(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def mk(p: mutable i64&, v: i64) -> void ensures p is Positive:
    p <- v
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_unproven.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("a non-constant write should leave the postcondition unproven (warned), got:\n%s", allDiagnostics(result))
	}
	strict := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_unproven_strict.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(strict.Errors()) == 0 {
		t.Fatalf("under -strict an unprovable postcondition should be a hard error")
	}
}

// --- Mutable refinement flow brick 3: preserve-credit. A ref call the callee provably doesn't use
// to mutate the argument KEEPS the caller's refinement facts. ---

// An immutable-borrow call (`p: i64&`) cannot mutate the argument, so a narrowed fact survives it —
// the obligation after the call still proves statically (no runtime check).
func TestPreserveCreditImmutableBorrow(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def observe(p: i64&) -> void:
    pass

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Positive:
        observe(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "preserve_immutable_borrow.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("immutable-borrow call should preserve the Positive fact, got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("fact should survive an immutable-borrow call (no runtime check), got %d", len(result.CallArgRefinementChecks))
	}
}

// SOUNDNESS GUARD: the canonical mutable borrow `p: mutable i64&` CAN mutate, so the fact is still
// dropped (a runtime check is forced) — preserve-credit must not over-apply.
func TestPreserveCreditMutableBorrowStillDrops(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def touch(p: mutable i64&) -> void:
    pass

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Positive:
        touch(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "preserve_mutable_borrow_drops.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("a mutable-borrow call must still DROP the fact (preserve-credit must not over-apply)")
	}
}

// An explicit `ensures p => preserve` postcondition grants preserve-credit even for a mutable
// borrow: the callee guarantees the argument is unchanged, so the fact survives.
func TestPreserveCreditExplicitEnsuresPreserve(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def keep(p: mutable i64&) -> void ensures p => preserve:
    pass

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Positive:
        keep(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "preserve_explicit_ensures.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`ensures p => preserve` should preserve the fact, got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("explicit preserve should keep the fact (no runtime check), got %d", len(result.CallArgRefinementChecks))
	}
}

// --- Mutable refinement flow brick 4: parametric facts. `if x is Bounded[0,500]:` carries a fact
// keyed by the constant bounds, usable across mutations, with exact-match (no over-proving). ---

// A parametric fact rides a MUTABLE variable: `if n is Bounded[0,500]:` proves a later
// `Bounded[0,500]` obligation on n with no runtime check.
func TestParametricMutableFactProves(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def needs_b(x: i64 is Bounded[0, 500]) -> i64:
    return x

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Bounded[0, 500]:
        return needs_b(n)
    return 0
`
	result := analyzeTreeTestSource(t, "param_mut_fact_proves.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("parametric fact on mutable n should prove needs_b(n), got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("parametric mutable fact should prove statically, got %d runtime checks", len(result.CallArgRefinementChecks))
	}
}

// SOUNDNESS: a parametric fact discharges only the SAME bounds — `Bounded[0,500]` must NOT prove a
// `Bounded[10,20]` obligation (exact key match, no cross-bounds entailment).
func TestParametricMutableFactExactMatchOnly(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def needs_tight(x: i64 is Bounded[10, 20]) -> i64:
    return x

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Bounded[0, 500]:
        return needs_tight(n)
    return 0
`
	result := analyzeTreeTestSource(t, "param_mut_fact_exact.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("`Bounded[0,500]` must NOT prove the tighter `Bounded[10,20]` — a runtime check is required")
	}
}

// A mutation drops a parametric fact just like a bare one.
func TestParametricMutableFactDroppedByMutation(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def needs_b(x: i64 is Bounded[0, 500]) -> i64:
    return x

def f(seed: i64, other: i64) -> i64:
    n: mutable i64 = seed
    if n is Bounded[0, 500]:
        n <- other
        return needs_b(n)
    return 0
`
	result := analyzeTreeTestSource(t, "param_mut_fact_drop.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("mutation `n <- other` must drop the parametric fact, forcing a runtime check")
	}
}
