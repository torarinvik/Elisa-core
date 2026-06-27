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

// COMPLETENESS-POSITIVE: two differently-instantiated aliases (`SlotIndex[cap128]` and
// `SlotIndex[cap256]`) are independent; values within their respective bounds prove statically.
func TestParametricTypeAliasRefinementTwoInstances(t *testing.T) {
	src := `
law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi

type SlotIndex[cap] = u32 is InRange[0, cap]

const cap128: u32 = 128
const cap256: u32 = 256

def f() -> u32:
    a: SlotIndex[cap128] = 10
    b: SlotIndex[cap256] = 200
    return a + b
`
	result := analyzeTreeTestSource(t, "param_alias_two_instances.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("two independently-instantiated parametric aliases should analyze cleanly, got: %v", errs)
	}
}

// SOUNDNESS-NEGATIVE: a constant that is OUT of the instantiated range is a compile-time refutation,
// not silently accepted. `SlotIndex[cap128]` (cap128=128) with value 200 must be rejected.
func TestParametricTypeAliasRefinementConstRefuted(t *testing.T) {
	src := `
law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi

type SlotIndex[cap] = u32 is InRange[0, cap]

const cap128: u32 = 128

def f() -> u32:
    x: SlotIndex[cap128] = 200
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "param_alias_const_refuted.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") && len(result.RefinementChecks) == 0 {
		t.Fatalf("constant 200 violates InRange[0,128]: expected refutation or runtime check, got:\n%s", allDiagnostics(result))
	}
}

// SOUNDNESS-NEGATIVE: `SlotIndex[cap128]` (cap128=128) does NOT prove `InRange[0,127]` — the
// instantiated hi bound is 128, so a value at exactly 128 would violate the tighter bound. A
// non-const value typed as `SlotIndex[cap128]` may be in [0,128]; discharging `InRange[0,127]`
// requires a runtime check.
func TestParametricTypeAliasDoesNotOverproveNarrower(t *testing.T) {
	src := `
law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi

type SlotIndex[cap] = u32 is InRange[0, cap]

const cap128: u32 = 128

def needs_127(x: u32 is InRange[0, 127]) -> u32: return x

def f(n: SlotIndex[cap128]) -> u32:
    return needs_127(n)
`
	result := analyzeTreeTestSource(t, "param_alias_no_overprove.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a SlotIndex[cap128] param should parse and typecheck, got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("SlotIndex[cap128] (bound [0,128]) must NOT statically prove InRange[0,127] — runtime check expected")
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
    x: i64 is Bounded[0..=500] = 600
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
        x: i64 is Bounded[0..=500] = a
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
        x: i64 is Bounded[0..=500] = a
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

// --- docs/90 brick 90-11: `ensures <param> is Law` seeds the caller's INTERVAL store, not only the
// factset. This lets the flow prover use the mutated variable's numeric bound — discharging an
// obligation the factset (exact-key) path cannot. ---

// After `clamp(&n)` with `ensures n is Bounded[0, 9]`, the caller knows n ∈ [0, 9]. A subsequent
// obligation for the WIDER `Bounded[0, 20]` proves only via interval entailment ([0,9] ⊆ [0,20]) —
// the factset holds the exact key Bounded[0,9], never Bounded[0,20], so this is the interval seed at
// work, not the predicate-fact gain.
func TestEnsuresParamSeedsCallerInterval(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def clamp(p: mutable i64&) -> void ensures p is Bounded[0, 9]:
    p <- 0

def use(seed: i64) -> i64:
    n: mutable i64 = seed
    clamp(&n)
    m: i64 is Bounded[0, 20] = n
    return m
`
	result := analyzeTreeTestSource(t, "ensures_param_interval_seed.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`ensures n is Bounded[0,9]` should let the WIDER `Bounded[0,20]` prove via interval, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("interval [0,9] entails Bounded[0,20]: expected static proof, got %d runtime checks", len(result.RefinementChecks))
	}
}

// Soundness floor for brick 90-11: mutating the variable AFTER the call drops the seeded interval
// (invalidateRangeFactsForTarget), so the same obligation falls back to a runtime check.
func TestEnsuresParamIntervalDroppedOnMutation(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def clamp(p: mutable i64&) -> void ensures p is Bounded[0, 9]:
    p <- 0

def use(seed: i64) -> i64:
    n: mutable i64 = seed
    clamp(&n)
    n <- seed
    m: i64 is Bounded[0, 20] = n
    return m
`
	result := analyzeTreeTestSource(t, "ensures_param_interval_drop.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected analysis to succeed with a runtime fallback, got: %v", errs)
	}
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("re-mutating n must drop the seeded interval, forcing a runtime check")
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

// Preserve-credit now reads the immutable-borrow signal from the resolved FuncType, so it applies to
// METHOD calls too (not just direct by-name calls): a method with an immutable-borrow param `p: i64&`
// cannot mutate the argument, so the caller's narrowed fact survives `recv.observe(&n)`.
func TestPreserveCreditImmutableBorrowMethodCall(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

struct Logger:
    count: i64

impl Logger:
    def observe(self: Logger, p: i64&) -> void:
        pass

def needs_pos(x: i64 is Positive) -> i64:
    return x

def f(seed: i64) -> i64:
    lg: mutable Logger = zeroed
    n: mutable i64 = seed
    if n is Positive:
        lg.observe(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "preserve_immutable_borrow_method.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("immutable-borrow METHOD call should preserve the Positive fact, got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("fact should survive an immutable-borrow method call (no runtime check), got %d", len(result.CallArgRefinementChecks))
	}
}

// SOUNDNESS GUARD: a method with a MUTABLE-borrow param still DROPS the fact through a method call.
func TestPreserveCreditMutableBorrowMethodStillDrops(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

struct Logger:
    count: i64

impl Logger:
    def touch(self: Logger, p: mutable i64&) -> void:
        pass

def needs_pos(x: i64 is Positive) -> i64:
    return x

def f(seed: i64) -> i64:
    lg: mutable Logger = zeroed
    n: mutable i64 = seed
    if n is Positive:
        lg.touch(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "preserve_mutable_borrow_method_drops.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("a mutable-borrow method call must still DROP the fact (preserve-credit must not over-apply)")
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

// --- Mutable refinement flow: dependent length facts (docs/85 §5.3 dependence-freeze). A fact whose
// bounds read a frozen variable (`i is Bounded[0, xs.count]`) discharges a same-scope obligation and
// is dropped when either the holder OR a dependency mutates. ---

// A dependent fact `i is Bounded[0, xs.count]` proves a same-scope obligation with the identical
// dependent bound — no runtime check — while xs is frozen.
func TestDependentLengthFactProves(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def f(xs: darray[i64], seed: i64) -> void:
    i: mutable i64 = seed
    if i is Bounded[0, xs.count]:
        j: i64 is Bounded[0, xs.count] = i
    return
`
	result := analyzeTreeTestSource(t, "dep_len_proves.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("dependent fact should prove the same-bound obligation, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("dependent fact should prove statically (no runtime check), got %d", len(result.RefinementChecks))
	}
}

// The exclusive sugar `Bounded[0..<xs.count]` parses as a dependent param refinement (docs/85 §5.3),
// desugaring to `Bounded[0, xs.count - 1]` — the doc's canonical dependent-bound form.
func TestDependentLengthExclusiveSugarParses(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def f(xs: darray[i64], i: i64 is Bounded[0..<xs.count]) -> void:
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dep_len_exclusive.elisa", src, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`Bounded[0..<xs.count]` dependent param refinement should parse cleanly, got: %v", errs)
	}
}

// SOUNDNESS (freeze): mutating the DEPENDENCY drops the dependent fact even though the holder `i` was
// untouched — a stale bound must not discharge. (Scalar dependency `n` keeps the test arena-free.)
func TestDependentLengthFactDroppedByDependencyMutation(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def f(n: mutable i64, seed: i64, other: i64) -> void:
    i: mutable i64 = seed
    if i is Bounded[0, n]:
        n <- other
        j: i64 is Bounded[0, n] = i
    return
`
	result := analyzeTreeTestSource(t, "dep_len_dep_mutated.elisa", src)
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("mutating the dependency `n` must drop the dependent fact, forcing a runtime check")
	}
}

// SOUNDNESS (holder): mutating the holder `i` also drops the dependent fact, like any pred fact.
func TestDependentLengthFactDroppedByHolderMutation(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def f(xs: darray[i64], seed: i64, other: i64) -> void:
    i: mutable i64 = seed
    if i is Bounded[0, xs.count]:
        i <- other
        j: i64 is Bounded[0, xs.count] = i
    return
`
	result := analyzeTreeTestSource(t, "dep_len_holder_mutated.elisa", src)
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("mutating the holder `i` must drop the dependent fact, forcing a runtime check")
	}
}

// SOUNDNESS (cross-function): a caller's dependent fact must NOT discharge a callee argument
// obligation by mere name coincidence — the callee's `xs` is a different variable.
func TestDependentLengthFactNotMatchedAcrossCall(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def needs_idx(xs: darray[i64], x: i64 is Bounded[0, xs.count]) -> void:
    return

def f(xs: darray[i64], seed: i64) -> void:
    i: mutable i64 = seed
    if i is Bounded[0, xs.count]:
        needs_idx(xs, i)
    return
`
	result := analyzeTreeTestSource(t, "dep_len_cross_call.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("a dependent fact must NOT discharge across a call boundary by name coincidence; a runtime check is required")
	}
}

// --- Mutable refinement flow: non-int written-const. The written-constant channel tracks any
// const-evaluable kind (bool, enum), not just integers, through ref pointees and plain writes. ---

// A BOOL written constant proves a bool law: `ready <- true` makes `ensures ready is On` provable.
func TestEnsuresRefinementProvenByWrittenBool(t *testing.T) {
	src := `
law On(self: bool) = self
def arm(p: mutable bool&) -> void ensures p is On:
    p <- true
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_bool_proven.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`p <- true` should statically prove `ensures p is On`, got: %v", result.Errors())
	}
}

// A BOOL written constant that violates the law is refuted (compile error), not merely warned.
func TestEnsuresRefinementRefutedByWrittenBool(t *testing.T) {
	src := `
law On(self: bool) = self
def disarm(p: mutable bool&) -> void ensures p is On:
    p <- false
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_bool_refuted.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("`p <- false` should REFUTE `ensures p is On` (not just warn), got:\n%s", allDiagnostics(result))
	}
}

// An ENUM written constant proves an enum law: `d <- Door.Open` makes `ensures d is Opened` provable.
// (Const-enum members const-evaluate; plain `enum` members do not const-fold in the engine yet, so the
// channel — which tracks any const-evaluable RHS — covers const enums here.)
func TestEnsuresRefinementProvenByWrittenEnum(t *testing.T) {
	src := `
const enum Door of u8:
    Open
    Shut

law Opened(self: Door) = self == Door.Open
def open_it(p: mutable Door&) -> void ensures p is Opened:
    p <- Door.Open
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_enum_proven.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`p <- Door.Open` should statically prove `ensures p is Opened`, got: %v", result.Errors())
	}
}

// An ENUM written constant that violates the law is refuted.
func TestEnsuresRefinementRefutedByWrittenEnum(t *testing.T) {
	src := `
const enum Door of u8:
    Open
    Shut

law Opened(self: Door) = self == Door.Open
def shut_it(p: mutable Door&) -> void ensures p is Opened:
    p <- Door.Shut
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_enum_refuted.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("`p <- Door.Shut` should REFUTE `ensures p is Opened`, got:\n%s", allDiagnostics(result))
	}
}

// A PARAMETRIC postcondition `ensures p is Bounded[0, 500]` is proven by a written constant in range.
func TestEnsuresParametricRefinementProvenByWrittenConst(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def clamp(p: mutable i64&) -> void ensures p is Bounded[0, 500]:
    p <- 42
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_param_proven.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`p <- 42` should statically prove `ensures p is Bounded[0, 500]`, got: %v", result.Errors())
	}
}

// The range-sugar form `Bounded[0..=500]` desugars to the same two endpoints.
func TestEnsuresParametricRefinementRangeSugarProven(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def clamp(p: mutable i64&) -> void ensures p is Bounded[0..=500]:
    p <- 500
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_param_range_proven.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`p <- 500` should statically prove `ensures p is Bounded[0..=500]`, got: %v", result.Errors())
	}
}

// A written constant OUT of the parametric bounds is refuted.
func TestEnsuresParametricRefinementRefutedByWrittenConst(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def clamp(p: mutable i64&) -> void ensures p is Bounded[0, 500]:
    p <- 600
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_param_refuted.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("`p <- 600` should REFUTE `ensures p is Bounded[0, 500]`, got:\n%s", allDiagnostics(result))
	}
}

// The arg count must match the law's non-subject parameters.
func TestEnsuresParametricRefinementArgCountMismatch(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def clamp(p: mutable i64&) -> void ensures p is Bounded[0]:
    p <- 1
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_param_argcount.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "expects 2 argument") {
		t.Fatalf("`Bounded[0]` should error on arg count, got:\n%s", allDiagnostics(result))
	}
}

// A caller GAINS the parametric fact across a call whose callee declares the matching postcondition.
func TestEnsuresParametricRefinementCallerGain(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
def clamp(p: mutable i64&) -> void ensures p is Bounded[0, 500]:
    p <- 1
    return
def needs(q: i64 is Bounded[0, 500]) -> void:
    return
def caller() -> void:
    x: mutable i64 = 9999
    clamp(&x)
    needs(x)
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_param_caller_gain.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("caller should GAIN `Bounded[0, 500]` on x after clamp(&x) to satisfy needs(x), got: %v", result.Errors())
	}
}

// A PLAIN (non-packed) enum written constant proves the law: plain enum variants without payloads
// are now const-folded (their tag value), so the written-const channel reasons about them too.
func TestEnsuresRefinementProvenByWrittenPlainEnum(t *testing.T) {
	src := `
enum Door:
    Open
    Shut

law Opened(self: Door) = self == Door.Open
def open_it(p: mutable Door&) -> void ensures p is Opened:
    p <- Door.Open
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_plain_enum_proven.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`p <- Door.Open` (plain enum) should statically prove `ensures p is Opened`, got: %v", result.Errors())
	}
}

// A PLAIN enum written constant that violates the law is refuted.
func TestEnsuresRefinementRefutedByWrittenPlainEnum(t *testing.T) {
	src := `
enum Door:
    Open
    Shut

law Opened(self: Door) = self == Door.Open
def shut_it(p: mutable Door&) -> void ensures p is Opened:
    p <- Door.Shut
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_plain_enum_refuted.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("`p <- Door.Shut` (plain enum) should REFUTE `ensures p is Opened`, got:\n%s", allDiagnostics(result))
	}
}

// --- tier-2: bounded linear arithmetic (docs/86) ---------------------------------------------

// Tier-2 proves a DERIVED affine subject: `tx*64 + ty` with tx,ty ∈ [0,63] is bounded by
// [0, 4095], discharging the return refinement with NO runtime check. Tier-1 cannot — the
// subject is an expression, not a bare variable.
func TestLinearDerivedIndexProven(t *testing.T) {
	src := boundedLaw + `
def tile_index(tx: i64, ty: i64) -> i64 is Bounded[0, 4095]:
    if tx >= 0 and tx <= 63 and ty >= 0 and ty <= 63:
        return tx * 64 + ty
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "linear_index.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("64*tx + ty over [0,63]² ⊆ [0,4095] should prove via tier-2, got: %v", result.Errors())
	}
	var linear int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenLinear {
			linear++
		}
	}
	if linear == 0 {
		t.Fatalf("the derived-index discharge should be recorded as proven (linear), got report=%+v", result.ProofReport)
	}
}

// Soundness: without the upper-bound guard, the affine bound is open on top and tier-2 must
// DECLINE (stays unproven → runtime check / strict error), never over-prove.
func TestLinearDerivedIndexInsufficientGuardDeclines(t *testing.T) {
	src := boundedLaw + `
def tile_index(tx: i64, ty: i64) -> i64 is Bounded[0, 4095]:
    if tx >= 0 and ty >= 0:
        return tx * 64 + ty
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "linear_index_weak.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("an unbounded-above affine subject must NOT prove the top of [0,4095], got:\n%s", allDiagnostics(result))
	}
}

// A const module value folds into the affine form: `tx*MAPHEIGHT + ty` with MAPHEIGHT a const.
func TestLinearDerivedIndexFoldsNamedConst(t *testing.T) {
	src := boundedLaw + `
const module Map:
    MAPHEIGHT: i64 = 64

def tile_index(tx: i64, ty: i64) -> i64 is Bounded[0, 4095]:
    if tx >= 0 and tx <= 63 and ty >= 0 and ty <= 63:
        return tx * Map::MAPHEIGHT + ty
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "linear_index_const.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("a named const coefficient should fold into the affine form and prove, got: %v", result.Errors())
	}
}

// --- tier-2 brick 86-2: param-refinement seeding (docs/86) -----------------------------------

// A refinement-typed param (via alias) carries its bound on ENTRY: the docs/85 §13 form proves
// with NO body guard — tx,ty ∈ [0,63] seed the range facts, tier-2 bounds tx*64+ty ⊆ [0,4095].
func TestParamRefinementSeedsDerivedIndex(t *testing.T) {
	src := boundedLaw + `
type TileX = i64 is Bounded[0, 63]
type TileY = i64 is Bounded[0, 63]

def tile_index(tx: TileX, ty: TileY) -> i64 is Bounded[0, 4095]:
    return tx * 64 + ty
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "param_seed_index.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("refinement-typed params should seed entry bounds and prove the derived index, got: %v", result.Errors())
	}
	var linear int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenLinear {
			linear++
		}
	}
	if linear == 0 {
		t.Fatalf("the seeded derived index should be proven (linear), got report=%+v", result.ProofReport)
	}
}

// Seeding also feeds tier-1: a bare refinement param proves a same-bound return with no guard.
func TestParamRefinementSeedsBareReturn(t *testing.T) {
	src := boundedLaw + `
def echo(n: i64 is Bounded[0, 100]) -> i64 is Bounded[0, 100]:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "param_seed_bare.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("a bare refinement param should seed its bound and prove a same-bound return, got: %v", result.Errors())
	}
}

// Soundness: a WIDER param refinement must NOT prove a tighter obligation. n ∈ [0,1000] does not
// entail [0,100] — the seed is exact, never over-wide.
func TestParamRefinementSeedDoesNotOverprove(t *testing.T) {
	src := boundedLaw + `
def narrow(n: i64 is Bounded[0, 1000]) -> i64 is Bounded[0, 100]:
    return n
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "param_seed_overprove.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("a [0,1000] param must not prove a [0,100] return; must stay unproven, got:\n%s", allDiagnostics(result))
	}
}

// Two parameters, each carrying a parametric refinement, must PARSE (the `,` in
// `Bounded[lo, hi]` previously collided with the parameter separator, breaking the
// param list) AND statically discharge a multiplicative return bound from both
// input bounds (31*4096 + 4095 == 131071) — the voice-state index proof shape.
func TestMultiParamRefinementParsesAndDischarges(t *testing.T) {
	src := `
law Bounded(self: usize, lo: usize, hi: usize) = self >= lo and self <= hi

def slot_index(rack: usize is Bounded[0, 31], voice: usize is Bounded[0, 4095]) -> usize is Bounded[0, 131071]:
    return rack * 4096 + voice
`
	result := analyzeTreeTestSource(t, "multi_param_refine.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("two-param refinement should parse and analyze cleanly, got:\n%s", allDiagnostics(result))
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("the multiplicative return bound should be PROVEN from the input bounds (no runtime check), got %d", len(result.RefinementChecks))
	}
}
