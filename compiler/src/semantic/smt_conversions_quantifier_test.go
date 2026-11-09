//go:build cgo

package semantic

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// `char` is physically stored as i64, but converting it to u8 uses LLVM truncation. The exact
// SMT model is unsigned modulo 256: this proves the byte range while preserving wrapping for
// out-of-range and negative char values.
func TestSMTModelsCharToU8Truncation(t *testing.T) {
	src := `
def byte_value(c: char) -> u8:
    ensure result >= 0
    ensure result <= 255
    return c.u8()
`
	r := analyzeContractStrict(t, "char_to_u8_truncation.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("char-to-u8 truncation should prove the u8 range, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestSMTProvesSViewIndexCharToU8Range(t *testing.T) {
	src := `
def byte_at(view: sview, index: usize) -> u8:
    requires index < view.len.usize()
    ensure result >= 0
    ensure result <= 255
    return view[index].u8()
`
	r := analyzeContractStrict(t, "sview_index_char_to_u8.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("sview[index].u8() should prove the byte range, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Soundness control: char is a 64-bit numeric type, not globally an 8-bit type. Narrowing to u8
// and widening back loses information for values outside 0..255 and must not prove identity.
func TestSMTDoesNotTreatCharAsGloballyByteSized(t *testing.T) {
	src := `
def roundtrip(c: char) -> i64:
    ensure result == c
    return c.u8().i64()
`
	r := analyzeContractStrict(t, "char_u8_wrapping_control.elisa", src)
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("char-to-u8-to-i64 loses high bits and must not prove identity, got: %v", r.Errors())
	}
}

// docs/85 gap #3: a fall-through after an early-return guard establishes the negated condition as a
// flow fact for the static provers (`if x < 0: return 0` ⟹ `x >= 0` afterwards; `if a == 0: return`
// ⟹ `a != 0` for unsigned). Combined with local-definition facts (#2) and SMT modulo reasoning, the
// align_up postcondition `result >= value` discharges under -strict. The 32-bit bounds are REQUIRED:
// `value + (alignment - rem)` can wrap for unbounded u64 (e.g. value=2^64-1, alignment=2 ⟹ result=0),
// so the unbounded postcondition is genuinely false — it discharges only with no-overflow preconditions.
func TestFallThroughGuardFactsDischargeAlignUp(t *testing.T) {
	src := `
def align_up(value: u64, alignment: u64) -> u64:
    requires value < 4294967296
    requires alignment < 4294967296
    ensure result >= value
    if alignment == 0:
        return value
    rem: u64 = value % alignment
    if rem == 0:
        return value
    return value + (alignment - rem)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "alignup.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("align_up's `ensure result >= value` should discharge via guard + local + modulo facts, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestSMTUnsignedWithinReservationOverflowGuard(t *testing.T) {
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; SMT discharge test skipped")
	}
	src := `
def within(addr: u64, length: u64, base: u64, cap: u64) -> bool:
    ensure result == (addr >= base and addr + length >= addr and addr + length <= base + cap)
    return addr >= base and addr + length >= addr and addr + length <= base + cap
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "u64_within_reservation.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("within-reservation overflow guard should discharge under SMT, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestSMTUnsignedOverflowClaimDeclinesWithCounterexample(t *testing.T) {
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; SMT discharge test skipped")
	}
	src := `
def unchecked(addr: u64, length: u64) -> u64:
    ensure addr + length >= addr
    return addr + length
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "u64_overflow_claim.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	if !strings.Contains(diags, "could not be proven") {
		t.Fatalf("unguarded overflowing u64 addition must not prove, got:\n%s", diags)
	}
	if !strings.Contains(diags, "counterexample") && !strings.Contains(diags, "can fail when") {
		t.Fatalf("unguarded overflowing u64 addition should report a counterexample, got:\n%s", diags)
	}
}

// The guard-narrowing soundness floor: a `<` guard establishes the post-guard lower bound, so a
// downstream `ensure result >= 0` on a clamp proves; nothing weaker is admitted.
func TestFallThroughGuardClampDischarges(t *testing.T) {
	src := `
def clamp_nonneg(x: i64) -> i64:
    ensure result >= 0
    if x < 0:
        return 0
    return x
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "clamp.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("clamp_nonneg should discharge via the fall-through guard fact, got:\n%s", strings.Join(errs, "\n"))
	}
}

// CONCEPT-SUGAR (docs/100): the `forall k in 0 ..< n: P` range binder desugars to the canonical guarded
// form `forall k: (0 <= k and k < n) implies P`. It must prove EXACTLY what the canonical form proves —
// `NotDouble[5]` holds for 11. This is the proof-of-equivalence for the pure parser desugar.
func TestSMTProvesQuantifierRangeSugar(t *testing.T) {
	src := `
law NotDouble(self: i64, n: i64) = forall k in 0 ..< n: self != k * 2

def pick() -> i64:
    x: i64 is NotDouble[5] = 11
    return x
`
	result := analyzeWithSMT(t, "smt_forall_sugar.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			proven++
		}
	}
	if proven != 1 {
		t.Fatalf("expected the range-sugar quantified law proven by SMT, got %d: %+v", proven, result.ProofReport)
	}
}

// Soundness: the range sugar must NOT prove a false law. `NotDouble[5]` is false for 4 (4 == 2*2).
func TestSMTDeclinesFalseQuantifierRangeSugar(t *testing.T) {
	src := `
law NotDouble(self: i64, n: i64) = forall k in 0 ..< n: self != k * 2

def pick() -> i64:
    x: i64 is NotDouble[5] = 4
    return x
`
	result := analyzeWithSMT(t, "smt_forall_sugar_false.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("SMT must not prove NotDouble[5] for 4 via range sugar: %+v", result.ProofReport)
		}
	}
}

// CONCEPT PREDICATE `Sorted` written with the range sugar over a real array subject (docs/100 Form 3).
// The law's ANTECEDENT uses the sugar `forall i in 0 ..< n - 1: self[i] <= self[i + 1]`; the law states
// that sortedness implies the first element is the minimum. z3 proves it via array theory — and because
// the sugar lowers to the identical canonical guarded form, it proves exactly as the hand-written
// `SortedFirstMin` theorem above does. This is the end-to-end proof that concept predicates ride on the
// sugar on array subjects with no new machinery.
func TestSMTProvesSortedConceptPredicate(t *testing.T) {
	src := `
law SortedFirstMinSugar(self: darray[i64], n: i64) = (forall i in 0 ..< n - 1: self[i] <= self[i + 1]) implies (forall j in 0 ..< n: self[0] <= self[j])

def check(xs: darray[i64]) -> i64:
    y: darray[i64] is SortedFirstMinSugar[10] = xs
    return 0
`
	result := analyzeWithSMT(t, "smt_sorted_sugar.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			proven++
		}
	}
	if proven != 1 {
		t.Fatalf("expected the sugar-written sorted theorem proven by SMT, got %d: %+v", proven, result.ProofReport)
	}
}

func TestSMTProvesSortedArrayFirstIsMinFromNaturalLaw(t *testing.T) {
	src := `
law Sorted(self: array[i64, 8]) = forall i in 0 ..< self.count - 1: self[i] <= self[i + 1]

def first_is_min(xs: array[i64, 8] is Sorted, j: i64) -> void:
    requires j >= 0 and j < xs.count
    assert xs[0] <= xs[j]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_sorted_first_min.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("sorted array should prove first element is <= any in-bounds element, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestSMTDeclinesFalseSortedArrayClaim(t *testing.T) {
	src := `
law SortedImpliesAllEqual(self: array[i64, 8]) = (forall i in 0 ..< self.count - 1: self[i] <= self[i + 1]) implies (forall j in 0 ..< self.count: self[0] == self[j])

def check(xs: array[i64, 8]) -> i64:
    y: array[i64, 8] is SortedImpliesAllEqual = xs
    return 0
`
	result := analyzeWithSMT(t, "smt_sorted_false_claim.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("false sorted-array equality claim must not be SMT-proven: %+v", result.ProofReport)
		}
	}
}

func TestSMTQuantifierSlicePrefixRangeSugar(t *testing.T) {
	src := `
law PrefixSorted(self: darray[i64], n: i64, k: i64) = ((forall i in 0 ..< n: self[i] <= self[i + 1]) and k <= n) implies (forall i in 0 ..< k: self[i] <= self[i + 1])

def check(xs: darray[i64]) -> i64:
    y: darray[i64] is PrefixSorted[10, 4] = xs
    return 0
`
	result := analyzeWithSMT(t, "smt_prefix_forall_sugar.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("slice-prefix range sugar theorem should analyze cleanly, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			proven++
		}
	}
	if proven != 1 {
		t.Fatalf("expected prefix theorem to be proven by SMT, got %d: %+v", proven, result.ProofReport)
	}
}

func TestSMTBoundedExistsNaturalSurface(t *testing.T) {
	src := `
law HasZero(self: array[i64, 8]) = exists i in 0 ..< self.count: self[i] == 0

def has_zero(xs: array[i64, 8]) -> bool:
    requires exists i in 0 ..< xs.count: xs[i] == 0
    return true
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_bounded_exists.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("bounded exists surface should analyze cleanly, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestSMTProvesArrayValueBindingQuantifierLowerBound(t *testing.T) {
	src := `
law AllPositive(self: darray[i64]) = forall x in self: x > 0

def first_is_lower_bound(xs: darray[i64], j: i64) -> void:
    requires xs.count > 0 and j >= 0 and j < xs.count
    requires forall x in xs: x >= xs[0]
    assert xs[j] >= xs[0]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_value_binding_lower_bound.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("value-binding quantifier should prove indexed lower-bound assertion, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestSMTDeclinesFalseArrayValueBindingQuantifierClaim(t *testing.T) {
	src := `
law AllEqualFirst(self: darray[i64]) = forall x in self: x == self[0]

def check(xs: darray[i64]) -> i64:
    y: darray[i64] is AllEqualFirst = xs
    return 0
`
	result := analyzeWithSMT(t, "smt_value_binding_false.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("false value-binding array claim must not be SMT-proven: %+v", result.ProofReport)
		}
	}
}

func TestQuantifierSetAndDictBindingsAreSpecOnly(t *testing.T) {
	src := `
law NoZeros(self: set[i64]) = forall x in self: x != 0
law DictNoZeros(self: dict[i64, i64]) = forall (k, v) in self: k != 0 and v != 0
law HasNonZero(self: set[i64]) = exists x in self: x != 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_set_dict_quantifier_surface.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("set/dict quantifier surface should analyze as spec-only, got:\n%s", strings.Join(errs, "\n"))
	}
}

// QUANTIFIER-INSTANTIATION STABILITY (feature/quantifier-triggers). The `In`-source quantifier path
// (`forall x in xs`, `forall i in xs.indices`) now carries a `(select arr idx)` `:pattern` trigger so
// z3 gets a deterministic E-matching instantiation instead of a bare quantifier. These three tests
// pin the three behaviours the trigger work must preserve: a heavy proof still discharges, a
// pathological quantifier DECLINES within the per-query timeout (no hang), and an established law
// does not regress.

// (a) A quantifier-heavy goal still discharges: a value-binding antecedent (`forall x in xs: x >= xs[0]`,
// the `In` path with the new select-trigger) combined with an index-quantifier consequence. With the
// trigger guiding instantiation z3 picks `xs[j]` from the universally-bound element fact and proves the
// indexed lower bound. The point is that the added `:pattern` does not break instantiation on a real goal.
func TestSMTQuantifierTriggerHeavyProofDischarges(t *testing.T) {
	src := `
law AllAtLeastFirst(self: darray[i64]) = forall x in self: x >= self[0]

def lower_bounded(xs: darray[i64], j: i64) -> void:
    requires xs.count > 0 and j >= 0 and j < xs.count
    requires forall x in xs: x >= xs[0]
    requires forall i in xs.indices: xs[i] >= 0
    assert xs[j] >= xs[0] and xs[j] >= 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_trigger_heavy.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("trigger-heavy quantifier proof should discharge, got:\n%s", strings.Join(errs, "\n"))
	}
}

// (b) A contrived HARD quantifier must DECLINE cleanly within the per-query timeout rather than hang.
// The universal `forall k in 0 ..< n: self * self != k` is FALSE for self=3 (the witness k = 9 lies in
// the range), and the nonlinear `self * self` keeps the falsifier off the trivial linear path — so z3
// must search for the instance under MBQI/E-matching. Either it finds the model (sat → decline) or hits
// the 2s per-query ceiling (unknown → decline); both are sound DECLINES, never a false proof.
// Soundness: it must NOT be SMT-proven, and the analysis must return well within the timeout budget.
func TestSMTHardQuantifierDeclinesWithinTimeout(t *testing.T) {
	src := `
law HardNonlinear(self: i64, n: i64) = forall k in 0 ..< n: self * self != k

def pick() -> i64:
    x: i64 is HardNonlinear[1000000] = 3
    return x
`
	start := time.Now()
	result := analyzeWithSMT(t, "smt_hard_quantifier.elisa", src)
	elapsed := time.Since(start)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("hard quantifier must not be SMT-proven (it can be false): %+v", result.ProofReport)
		}
	}
	// The per-query timeout is 2s; the whole analysis (spawn + this one query) must finish well within a
	// generous multiple of it. A wedged solver would blow past this; the assertion is the no-hang gate.
	if elapsed > 20*time.Second {
		t.Fatalf("hard quantifier query did not decline within the timeout budget: took %s", elapsed)
	}
}

// (c) No regression: an established value-binding quantifier law still proves after the trigger change.
// `forall x in xs: x > 0` (the `In` path the trigger now annotates) must still discharge the indexed
// positivity assertion exactly as before.
func TestSMTQuantifierTriggerNoRegression(t *testing.T) {
	src := `
law AllPositive(self: darray[i64]) = forall x in self: x > 0

def index_is_positive(xs: darray[i64], j: i64) -> void:
    requires j >= 0 and j < xs.count
    requires forall x in xs: x > 0
    assert xs[j] > 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "smt_trigger_no_regression.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("established value-binding quantifier law must still prove, got:\n%s", strings.Join(errs, "\n"))
	}
}
