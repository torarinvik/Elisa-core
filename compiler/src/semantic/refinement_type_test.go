//go:build cgo

package semantic

import (
	"testing"
)

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

// docs/90 brick 90-7: a callee's REFINED return type is its postcondition; binding the result of a
// direct call lets the caller assume that bound as a flow fact. Here `clamp() -> i64 is Bounded[0,
// 100]` makes `x` provably in [0, 100], so the downstream `y: i64 is Nat = x` discharges statically
// with NO runtime check — the caller never re-derives the bound from clamp's body.
func TestReturnRefinementSeedsCallerFact(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
law Nat(self: i64) = self >= 0

def clamp() -> i64 is Bounded[0, 100]:
    return 50

def use() -> i64:
    x = clamp()
    y: i64 is Nat = x
    return y
`
	result := analyzeTreeTestSource(t, "return_refine_seed.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("return-refinement seed should analyze cleanly, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("Nat on a value bound from Bounded[0,100] should be proven via the seeded fact, got %d runtime checks", len(result.RefinementChecks))
	}
}

// Soundness floor for brick 90-7: with NO refinement on the callee's return type, binding its result
// seeds nothing, so a downstream refinement obligation correctly falls back to a runtime check.
func TestReturnRefinementNoSeedWhenUnrefined(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def anyInt() -> i64:
    return 50

def use() -> i64:
    x = anyInt()
    y: i64 is Nat = x
    return y
`
	result := analyzeTreeTestSource(t, "return_refine_noseed.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unrefined return should analyze (runtime fallback), got: %v", errs)
	}
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("with no return refinement there is no caller fact: expected a runtime check")
	}
}

// docs/90 brick 90-8: a PARAMETRIC return refinement (`-> i64 is Bounded[0, n]`) names a callee
// param in its bound; at a call site the param is substituted by the caller's argument, so
// `cap_to(100)` yields the caller fact `[0, 100]` and the downstream `Nat` obligation discharges
// statically. The bound is resolved in the caller's terms, not re-derived from the callee body.
func TestParametricReturnRefinementSeedsCallerFact(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
law Nat(self: i64) = self >= 0

def cap_to(n: i64) -> i64 is Bounded[0, n]:
    return 0

def use() -> i64:
    x = cap_to(100)
    y: i64 is Nat = x
    return y
`
	result := analyzeTreeTestSource(t, "param_return_refine_seed.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("parametric return-refinement seed should analyze cleanly, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("Nat on a value bound from Bounded[0, n] with n=100 should be proven via the substituted fact, got %d runtime checks", len(result.RefinementChecks))
	}
}

// Soundness floor for brick 90-8: when the callee-param bound is substituted by a NON-constant caller
// argument, the bracket value is not statically known, so no fact is seeded and the downstream
// obligation falls back to a runtime check (never a fabricated bound).
func TestParametricReturnRefinementNonConstArgNoSeed(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
law Nat(self: i64) = self >= 0

def cap_to(n: i64) -> i64 is Bounded[0, n]:
    return 0

def use(m: i64) -> i64:
    x = cap_to(m)
    y: i64 is Nat = x
    return y
`
	result := analyzeTreeTestSource(t, "param_return_refine_nonconst.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("non-const parametric return refinement should analyze (runtime fallback), got: %v", errs)
	}
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("a non-constant bracket argument is not statically known: expected a runtime check")
	}
}

// docs/90 brick 90-9: a parametric return refinement whose bracket argument is a NON-constant caller
// value with a KNOWN interval is resolved direction-aware. `cap_to(k)` with `k <= 10` makes the
// upper bound `self <= n` contribute `self <= 10`, so `x ∈ [0, 10]` and the downstream `AtMost10`
// obligation discharges with no runtime check — without `k` ever being a compile-time constant.
func TestRangedReturnRefinementSeedsCallerFact(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
law AtMost10(self: i64) = self <= 10

def cap_to(n: i64) -> i64 is Bounded[0, n]:
    return 0

def use(k: i64 is AtMost10) -> i64:
    x = cap_to(k)
    y: i64 is AtMost10 = x
    return y
`
	result := analyzeTreeTestSource(t, "ranged_return_refine_seed.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("ranged return-refinement seed should analyze cleanly, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("AtMost10 on a value bound from Bounded[0, n] with n<=10 should be proven via the ranged fact, got %d runtime checks", len(result.RefinementChecks))
	}
}

// Soundness floor for brick 90-9: when the bracket-argument caller value has NO known interval (a
// plain i64 param), the ranged fallback resolves nothing on the needed side, so no fact is seeded and
// the downstream obligation falls back to a runtime check.
func TestRangedReturnRefinementNoBoundNoSeed(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
law AtMost10(self: i64) = self <= 10

def cap_to(n: i64) -> i64 is Bounded[0, n]:
    return 0

def use(k: i64) -> i64:
    x = cap_to(k)
    y: i64 is AtMost10 = x
    return y
`
	result := analyzeTreeTestSource(t, "ranged_return_refine_nobound.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unbounded bracket argument should analyze (runtime fallback), got: %v", errs)
	}
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("a bracket argument with no known interval is not statically usable: expected a runtime check")
	}
}

// docs/90 brick 90-10: a value-contract postcondition over `result` (`ensure result >= 0`) is a
// promise about the returned value; binding the result lets the caller assume it as a flow fact, just
// like a refined return type. `x = make()` therefore satisfies `Nat` with no runtime check.
func TestEnsureResultPostconditionSeedsCallerFact(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def make() -> i64:
    ensure result >= 0
    return 5

def use() -> i64:
    x = make()
    y: i64 is Nat = x
    return y
`
	result := analyzeTreeTestSource(t, "ensure_result_seed.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`ensure result >= 0` should let `x is Nat` prove, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("Nat on a value with postcondition `result >= 0` should be proven, got %d runtime checks", len(result.RefinementChecks))
	}
}

// Brick 90-10 with a PARAMETRIC postcondition: `ensure result <= n` resolves the bound from the
// caller's argument, so `clamp(50)` yields `result ∈ [0, 50]`, discharging a `Bounded[0, 50]`
// obligation statically.
func TestEnsureResultParametricPostconditionSeedsCallerFact(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def clamp(n: i64) -> i64:
    ensure result >= 0
    ensure result <= n
    return 0

def use() -> i64:
    x = clamp(50)
    y: i64 is Bounded[0, 50] = x
    return y
`
	result := analyzeTreeTestSource(t, "ensure_result_param_seed.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("parametric `ensure result <= n` should let `x is Bounded[0,50]` prove, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("Bounded[0,50] on a value with postconditions result>=0, result<=50 should be proven, got %d runtime checks", len(result.RefinementChecks))
	}
}

// Soundness floor for brick 90-10: with NO value-contract postcondition, binding the result seeds
// nothing and the obligation falls back to a runtime check.
func TestEnsureResultNoPostconditionNoSeed(t *testing.T) {
	src := `
law Nat(self: i64) = self >= 0

def make() -> i64:
    return 5

def use() -> i64:
    x = make()
    y: i64 is Nat = x
    return y
`
	result := analyzeTreeTestSource(t, "ensure_result_noseed.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("no postcondition should analyze (runtime fallback), got: %v", errs)
	}
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("with no postcondition there is no caller fact: expected a runtime check")
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

// ── Parametric type-alias refinements ────────────────────────────────────────────────────────────
//
// `type Name[param] = base is Law[..param..]` declares a reusable bounded-index type where the
// bound is a value parameter. A use site `Name[N]` (GenericType) substitutes N→param in the
// predicate args and yields the concrete refinement.
//
// PARSER CONSTRAINT: due to the existing parser disambiguation between `Name[int_literal]`
// (= array type) and `Name[Ident]` (= generic type), parametric alias use sites must supply
// a CONSTANT IDENTIFIER or a function-parameter name, not an integer literal directly. For
// concrete instantiation with a known integer use a named constant:
//   cap128: i64 = 128    (const)
//   x: SlotIndex[cap128] = 64
// or a function generic-type param name `[N]` in a generic function context.

// COMPLETENESS-POSITIVE: `SlotIndex[cap128]` where cap128 is a known constant declares a value
// in [0, 128]; a constant 64 satisfies InRange[0,128] — proven statically with no runtime check.
func TestParametricTypeAliasRefinementConstProven(t *testing.T) {
	src := `
law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi

type SlotIndex[cap] = u32 is InRange[0, cap]

const cap128: u32 = 128

def f() -> u32:
    x: SlotIndex[cap128] = 64
    return x
`
	result := analyzeTreeTestSource(t, "param_alias_const_proven.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("parametric alias SlotIndex[cap128] (cap128=128) with constant 64 should prove statically, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("constant 64 satisfies InRange[0,128] — no runtime check expected, got %d", len(result.RefinementChecks))
	}
}
