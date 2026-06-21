//go:build cgo

package semantic

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func analyzeWithSMT(t *testing.T, filename, src string) *Result {
	t.Helper()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; SMT discharge test skipped")
	}
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	return AnalyzeWithOptions(file, AnalyzeOptions{EnableSMT: true})
}

// The headline win: a NON-LINEAR refinement the affine prover cannot reach. With a,b in [2,10],
// `a*b` is in [4,100] — but a*b is var*var, outside the affine fragment, so the linear tier declines.
// The SMT tier proves it.
func TestSMTProvesNonlinearReturn(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Small = i64 is Bounded[2, 10]

def mul(a: Small, b: Small) -> i64 is Bounded[4, 100]:
    return a * b
`
	result := analyzeWithSMT(t, "smt_nonlinear.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var smtProven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			smtProven++
		}
	}
	if smtProven != 1 {
		t.Fatalf("expected the nonlinear return to be proven by SMT, got %d: %+v", smtProven, result.ProofReport)
	}
	// The `Bounded` law body `self >= lo and self <= hi` is conjunctive, so multi-goal batching (VC IR
	// brick 4) splits it into two independent sub-goals — both prove, so the solver records 2 proven
	// queries for this one refinement obligation (the per-obligation ProofReport above is still 1).
	if !result.SMTProfile.Enabled || result.SMTProfile.Proven != 2 {
		t.Fatalf("expected SMT profile to record 2 proven (two batched conjuncts), got %+v", result.SMTProfile)
	}
}

// Soundness: an UNTRUE nonlinear bound is NOT proven (sat → declined, not a false proof). With
// a,b in [2,10], a*b can reach 100, so `Bounded[4, 50]` does not hold — the SMT tier must decline.
func TestSMTDeclinesFalseNonlinearBound(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Small = i64 is Bounded[2, 10]

def mul(a: Small, b: Small) -> i64 is Bounded[4, 50]:
    return a * b
`
	result := analyzeWithSMT(t, "smt_nonlinear_false.elisa", src)
	// Not an error (the obligation falls back to a runtime check + warning), but it must NOT be SMT-proven.
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("SMT must not prove a false bound (a*b can reach 100 > 50): %+v", result.ProofReport)
		}
	}
	// With brick-4 batching the conjunctive `Bounded[4, 50]` body splits: `self >= 4` proves but
	// `self <= 50` declines (a*b reaches 100). The overall refinement is therefore NOT proven (the
	// ProofReport check above is the soundness gate); the solver legitimately records >=1 declined.
	if result.SMTProfile.Declined < 1 {
		t.Fatalf("expected SMT to attempt and decline the false bound, got %+v", result.SMTProfile)
	}
}

// Operand-independent mask bound: `(word >> sh) & C` for a non-negative constant C is in [0, C]
// regardless of the RUNTIME shift `sh`, so a masked field extraction proves its width bound even
// though the variable shift puts the masked operand outside the modelable bitvector fragment. This
// is the emulator decode-firewall payoff for variable bit positions.
func TestSMTProvesRuntimeShiftMaskBound(t *testing.T) {
	src := `
law lt16(self: u32) = self < 16

def extract(word: u32, sh: u32) -> u32 is lt16:
    return (word >> sh) & 0xF
`
	result := analyzeWithSMT(t, "smt_mask_runtime_shift.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var smtProven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			smtProven++
		}
	}
	if smtProven != 1 {
		t.Fatalf("expected the runtime-shift mask bound proven by SMT, got %d: %+v", smtProven, result.ProofReport)
	}
}

// Soundness: the mask bound is an over-approximation to [0, C] — it must NOT prove a tighter range
// than the mask guarantees. `& 0xF` only bounds the result to [0, 15]; claiming `< 8` is false
// (e.g. `0xF >> 0 == 15`), so the prover must decline rather than fabricate a proof.
func TestSMTDeclinesTooTightMaskBound(t *testing.T) {
	src := `
law lt8(self: u32) = self < 8

def extract(word: u32, sh: u32) -> u32 is lt8:
    return (word >> sh) & 0xF
`
	result := analyzeWithSMT(t, "smt_mask_too_tight.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("SMT must not prove `<8` from a `& 0xF` mask (the value reaches 15): %+v", result.ProofReport)
		}
	}
}

// Signed bitwise: the sign-extension idiom `(field << k) >> k` (arithmetic right shift on a signed
// result) sign-extends a low-bit field to the full width. For a 12-bit field this yields the exact
// signed range [-2048, 2047]. The SMT tier models the signed read-back and `bvashr`, so the bound proves.
func TestSMTProvesSignExtensionRange(t *testing.T) {
	src := `
law SBounded(self: i32, lo: i32, hi: i32) = self >= lo and self <= hi

def sign_extend12(field: i32) -> i32 is SBounded[-2048, 2047]:
    requires field >= 0 and field <= 4095
    return (field << 20) >> 20
`
	result := analyzeWithSMT(t, "smt_sign_extend.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var smtProven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			smtProven++
		}
	}
	if smtProven != 1 {
		t.Fatalf("expected the sign-extension signed range proven by SMT, got %d: %+v", smtProven, result.ProofReport)
	}
}

// Soundness: sign-extension produces a value that CAN be negative (the high field bit becomes the sign),
// so a non-negative claim must NOT be proven. `(field << 20) >> 20` for field in [0,4095] reaches -2048.
func TestSMTDeclinesNonNegativeSignExtension(t *testing.T) {
	src := `
law SBounded(self: i32, lo: i32, hi: i32) = self >= lo and self <= hi

def sign_extend12(field: i32) -> i32 is SBounded[0, 2047]:
    requires field >= 0 and field <= 4095
    return (field << 20) >> 20
`
	result := analyzeWithSMT(t, "smt_sign_extend_bad.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("SMT must not prove a non-negative range for a sign-extension that reaches -2048: %+v", result.ProofReport)
		}
	}
}

// A same-width sign-reinterpret cast (`(u32 field).i32()`) is modeled exactly (the bitcast relating
// the unsigned and signed views), so a field extracted as unsigned and then sign-extended proves its
// signed range end to end — the emulator immediate-decode pattern.
func TestSMTProvesMaskedFieldSignExtension(t *testing.T) {
	src := `
law SBounded(self: i32, lo: i32, hi: i32) = self >= lo and self <= hi

def imm(word: u32) -> i32 is SBounded[-2048, 2047]:
    return ((((word >> 20) & 0xFFF).i32()) << 20) >> 20
`
	result := analyzeWithSMT(t, "smt_masked_signext.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var smtProven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			smtProven++
		}
	}
	if smtProven != 1 {
		t.Fatalf("expected the masked sign-extension proven by SMT, got %d: %+v", smtProven, result.ProofReport)
	}
}

// Soundness: a same-width unsigned->signed reinterpret is the EXACT two's-complement bitcast, not a
// value-preserving widening. A u32 that can exceed i32-max becomes negative as i32, so a non-negative
// claim must NOT prove (modeling the cast as identity here would be unsound).
func TestSMTDeclinesWrappingReinterpret(t *testing.T) {
	src := `
law NonNeg(self: i32) = self >= 0

def reinterpret(big: u32) -> i32 is NonNeg:
    return big.i32()
`
	result := analyzeWithSMT(t, "smt_wrapping_reinterpret.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("SMT must not prove `>= 0` for a u32->i32 reinterpret that can wrap negative: %+v", result.ProofReport)
		}
	}
}

// Division (docs/90 brick 3): `n / 2` for an unsigned n in [0,100] is in [0,50]. SMT-LIB `div` is
// Euclidean, which equals Elisa truncating division here because n >= 0 and the divisor is > 0.
func TestSMTProvesDivision(t *testing.T) {
	src := `
law Bounded(self: usize, lo: usize, hi: usize) = self >= lo and self <= hi
type Hundred = usize is Bounded[0, 100]

def half(n: Hundred) -> usize is Bounded[0, 50]:
    return n / 2
`
	result := analyzeWithSMT(t, "smt_division.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var smtProven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			smtProven++
		}
	}
	if smtProven != 1 {
		t.Fatalf("expected `n / 2` bound proven by SMT, got %d: %+v", smtProven, result.ProofReport)
	}
}

// Signed division: Elisa truncates toward zero, while SMT-LIB `div` is Euclidean.
// The SMT tier models truncation explicitly, so a signed dividend range can now prove a signed bound.
func TestSMTProvesSignedTruncatingDivision(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type SignedHundred = i64 is Bounded[-100, 100]

def half(n: SignedHundred) -> i64 is Bounded[-50, 50]:
    return n / 2
`
	result := analyzeWithSMT(t, "smt_signed_div.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var smtProven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			smtProven++
		}
	}
	if smtProven != 1 {
		t.Fatalf("expected signed truncating division bound proven by SMT, got %d: %+v", smtProven, result.ProofReport)
	}
}

// Soundness gate: even with truncating semantics modeled, a divisor that may be zero is NOT
// translated because SMT-LIB division is total at zero and source division is not.
func TestSMTDeclinesDivisionByMaybeZero(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type MaybeZero = i64 is Bounded[-1, 1]

def divide(n: i64, d: MaybeZero) -> i64 is Bounded[-100, 100]:
    return n / d
`
	result := analyzeWithSMT(t, "smt_div_maybe_zero.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("division by a maybe-zero divisor must not be SMT-proven: %+v", result.ProofReport)
		}
	}
}

// Signed modulo rides the same truncating quotient model: r = x - y * trunc_div(x, y). For divisor 3,
// Elisa/C-style remainder stays in [-2, 2] even for negative dividends.
func TestSMTProvesSignedTruncatingModulo(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type SignedHundred = i64 is Bounded[-100, 100]

def rem3(n: SignedHundred) -> i64 is Bounded[-2, 2]:
    return n % 3
`
	result := analyzeWithSMT(t, "smt_signed_mod.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var smtProven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			smtProven++
		}
	}
	if smtProven != 1 {
		t.Fatalf("expected signed truncating modulo bound proven by SMT, got %d: %+v", smtProven, result.ProofReport)
	}
}

// A NON-LINEAR precondition (docs/90 brick 3): `requires a * b <= 100`, called with a,b in [2,5]
// (a*b <= 25). The linear clause prover declines var*var; the SMT fallback proves it under the
// caller's facts.
func TestSMTProvesNonlinearRequires(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Five = i64 is Bounded[2, 5]

def needs(a: i64, b: i64) -> i64:
    requires a * b <= 100
    return a + b

def caller(a: Five, b: Five) -> i64:
    return needs(a, b)
`
	result := analyzeWithSMT(t, "smt_requires.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT && f.Predicate == "requires" {
			proven++
		}
	}
	if proven != 1 {
		t.Fatalf("expected the nonlinear precondition proven by SMT, got %d: %+v", proven, result.ProofReport)
	}
}

// Counterexample (docs/90 brick 3): a precondition the caller's facts do NOT guarantee yields a
// concrete witness in the warning. `requires a * b <= 10` called with a,b in [2,5] can reach 25.
func TestSMTCounterexampleInWarning(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Five = i64 is Bounded[2, 5]

def needs(a: i64, b: i64) -> i64:
    requires a * b <= 10
    return a + b

def caller(a: Five, b: Five) -> i64:
    return needs(a, b)
`
	result := analyzeWithSMT(t, "smt_counterexample.elisa", src)
	warnings := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(warnings, "it can fail when ") {
		t.Fatalf("expected a counterexample-bearing warning, got:\n%s", warnings)
	}
	// The witness must mention both argument variables with values reaching > 10.
	if !strings.Contains(warnings, "a=") || !strings.Contains(warnings, "b=") {
		t.Fatalf("expected the counterexample to name a and b, got:\n%s", warnings)
	}
}

// QUANTIFIERS (docs/90 brick 90-4): a `forall` law proven by SMT. `NotDouble[5]` holds for 11
// (11 is not 0,2,4,6,8). The affine/const tiers can't reason about an unbounded quantifier — only SMT.
func TestSMTProvesQuantifiedLaw(t *testing.T) {
	src := `
law NotDouble(self: i64, n: i64) = forall k: (0 <= k and k < n) implies self != k * 2

def pick() -> i64:
    x: i64 is NotDouble[5] = 11
    return x
`
	result := analyzeWithSMT(t, "smt_forall.elisa", src)
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
		t.Fatalf("expected the quantified law proven by SMT, got %d: %+v", proven, result.ProofReport)
	}
}

// Soundness: SMT must NOT prove a quantified law that fails. `NotDouble[5]` is false for 4 (4 == 2*2).
func TestSMTDeclinesFalseQuantifiedLaw(t *testing.T) {
	src := `
law NotDouble(self: i64, n: i64) = forall k: (0 <= k and k < n) implies self != k * 2

def pick() -> i64:
    x: i64 is NotDouble[5] = 4
    return x
`
	result := analyzeWithSMT(t, "smt_forall_false.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("SMT must not prove NotDouble[5] for 4 (4 == 2*2): %+v", result.ProofReport)
		}
	}
}

// Spec-only: with SMT off, a quantified refinement that cannot be proven warns (no runtime check —
// an unbounded quantifier is not executable) rather than erroring or emitting broken code.
func TestQuantifiedLawSpecOnlyWhenSMTOff(t *testing.T) {
	src := `
law NotDouble(self: i64, n: i64) = forall k: (0 <= k and k < n) implies self != k * 2

def pick(v: i64) -> i64:
    x: i64 is NotDouble[5] = v
    return x
`
	result := analyzeTreeTestSource(t, "smt_forall_off.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("quantified refinement should warn (not error) when SMT is off, got: %v", errs)
	}
	if !strings.Contains(strings.Join(result.Warnings(), "\n"), "quantified refinement") {
		t.Fatalf("expected a spec-only quantified-refinement warning, got: %v", result.Warnings())
	}
}

// ARRAY-ELEMENT QUANTIFIERS (docs/90 brick 90-5): a real theorem over an ABSTRACT array — sorted
// implies the first element is the minimum. No concrete contents; z3 proves it via array theory +
// quantifiers. self[i] is modeled as (select v_self i).
func TestSMTProvesArrayQuantifierTheorem(t *testing.T) {
	src := `
law SortedFirstMin(self: darray[i64], n: i64) = (forall i: (0 <= i and i < n - 1) implies self[i] <= self[i + 1]) implies (forall j: (0 <= j and j < n) implies self[0] <= self[j])

def check(xs: darray[i64]) -> i64:
    y: darray[i64] is SortedFirstMin[10] = xs
    return 0
`
	result := analyzeWithSMT(t, "smt_array_quant.elisa", src)
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
		t.Fatalf("expected the array-quantifier theorem proven by SMT, got %d: %+v", proven, result.ProofReport)
	}
}

// Soundness: a FALSE array-quantifier claim is not proven. "all elements equal element 0" does not
// follow for an arbitrary array.
func TestSMTDeclinesFalseArrayQuantifier(t *testing.T) {
	src := `
law AllEqualFirst(self: darray[i64], n: i64) = forall i: (0 <= i and i < n) implies self[i] == self[0]

def check(xs: darray[i64]) -> i64:
    y: darray[i64] is AllEqualFirst[10] = xs
    return 0
`
	result := analyzeWithSMT(t, "smt_array_quant_false.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("a false array-quantifier claim must not be SMT-proven: %+v", result.ProofReport)
		}
	}
}

func TestSMTProvesFixedArrayCountAndIndexQuantifier(t *testing.T) {
	src := `
law FixedSortedFirstMin(self: array[i64, 4]) = (forall i: (0 <= i and i < self.count - 1) implies self[i] <= self[i + 1]) implies (forall j: (0 <= j and j < self.count) implies self[0] <= self[j])

def check(xs: array[i64, 4]) -> i64:
    y: array[i64, 4] is FixedSortedFirstMin = xs
    return 0
`
	result := analyzeWithSMT(t, "smt_fixed_array_quant.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected fixed-array quantifier law to analyze cleanly, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			proven++
		}
	}
	if proven != 1 {
		t.Fatalf("expected fixed-array count/index quantifier theorem proven by SMT, got %d: %+v", proven, result.ProofReport)
	}
}

func TestSMTFixedArrayCountIsExact(t *testing.T) {
	src := `
law CountIsFour(self: array[i64, 4]) = self.count == 4

def check(xs: array[i64, 4]) -> i64:
    y: array[i64, 4] is CountIsFour = xs
    return 0
`
	result := analyzeWithSMT(t, "smt_fixed_array_count.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected fixed-array count law to analyze cleanly, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			proven++
		}
	}
	if proven != 1 {
		t.Fatalf("expected fixed-array count to be exact in SMT, got %d: %+v", proven, result.ProofReport)
	}
}

func TestSMTDeclinesFalseFixedArrayQuantifier(t *testing.T) {
	src := `
law AllEqualFirst(self: array[i64, 4]) = forall i: (0 <= i and i < self.count) implies self[i] == self[0]

def check(xs: array[i64, 4]) -> i64:
    y: array[i64, 4] is AllEqualFirst = xs
    return 0
`
	result := analyzeWithSMT(t, "smt_fixed_array_quant_false.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("a false fixed-array quantifier claim must not be SMT-proven: %+v", result.ProofReport)
		}
	}
}

// THE PAYOFF (docs/90 brick 90-6): a quantified PRECONDITION is assumed in the body and discharges a
// real indexed-value refinement. `requires forall k: 0<=k<n implies xs[k] >= 0` + `requires n > 0`
// prove `return xs[0]` satisfies `is NonNeg` — by quantifier instantiation at k=0.
func TestSMTQuantifiedRequiresDischargesReturn(t *testing.T) {
	src := `
law NonNeg(self: i64) = self >= 0

def first(xs: darray[i64], n: i64) -> i64 is NonNeg:
    requires n > 0
    requires forall k: (0 <= k and k < n) implies xs[k] >= 0
    return xs[0]
`
	result := analyzeWithSMT(t, "smt_req_quant.elisa", src)
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
		t.Fatalf("expected the return discharged from the quantified precondition, got %d: %+v", proven, result.ProofReport)
	}
}

// Soundness: WITHOUT the quantified precondition, `xs[0]` is not provably non-negative — the SMT tier
// must NOT prove it (it declines; the obligation falls back).
func TestSMTNoRequiresLeavesIndexUnproven(t *testing.T) {
	src := `
law NonNeg(self: i64) = self >= 0

def first(xs: darray[i64], n: i64) -> i64 is NonNeg:
    requires n > 0
    return xs[0]
`
	result := analyzeWithSMT(t, "smt_req_none.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("xs[0] must not be SMT-proven NonNeg without a precondition: %+v", result.ProofReport)
		}
	}
}

// CALLER-SIDE QUANTIFIED ARRAY PRECONDITIONS (docs/90 brick 90-13): a caller that itself carries a
// quantified array `requires` discharges a callee's identical precondition at the call site — the
// dual of 90-6. `forward` requires `forall k: 0<=k<m implies data[k] >= 0` and passes data/m to
// `consume`, which requires the same over its own params. The caller's requires is assumed as a
// hypothesis, and both clauses translate against the same array symbol, so the call site is PROVEN.
func TestSMTCallerQuantifiedArrayPreconditionProven(t *testing.T) {
	src := `
def consume(xs: darray[i64], n: i64) -> i64:
    requires forall k: (0 <= k and k < n) implies xs[k] >= 0
    return 0

def forward(data: darray[i64], m: i64) -> i64:
    requires forall k: (0 <= k and k < m) implies data[k] >= 0
    return consume(data, m)
`
	result := analyzeWithSMT(t, "smt_caller_array_req.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT && f.Predicate == "requires" {
			proven++
		}
	}
	if proven != 1 {
		t.Fatalf("expected the callee precondition proven from the caller's matching precondition, got %d: %+v", proven, result.ProofReport)
	}
}

// Soundness: WITHOUT a matching precondition on the caller, the callee's quantified array
// precondition must NOT be SMT-proven (the caller has no fact about the array contents). It declines
// to the runtime check (a warning), never a fabricated proof.
func TestSMTCallerQuantifiedArrayPreconditionDeclinesWithoutFact(t *testing.T) {
	src := `
def consume(xs: darray[i64], n: i64) -> i64:
    requires forall k: (0 <= k and k < n) implies xs[k] >= 0
    return 0

def forward(data: darray[i64], m: i64) -> i64:
    return consume(data, m)
`
	result := analyzeWithSMT(t, "smt_caller_array_req_none.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT && f.Predicate == "requires" {
			t.Fatalf("a callee array precondition must not be SMT-proven without a caller fact: %+v", result.ProofReport)
		}
	}
}

// BLOCK-FORM LAW BODIES (docs/90 brick 90-15): a law may name its sub-predicates in an indented
// block ending in `return`, instead of packing everything onto one `= <expr>` line. The bindings are
// inlined at parse time, so the SMT tier sees exactly the same single predicate. This is the
// sorted-implies-first-is-min theorem written readably.
func TestBlockFormLawProvesArrayQuantifierTheorem(t *testing.T) {
	src := `
law SortedFirstMin(self: darray[i64], n: i64):
    sorted = forall i: (0 <= i and i < n - 1) implies self[i] <= self[i + 1]
    firstMin = forall j: (0 <= j and j < n) implies self[0] <= self[j]
    return sorted implies firstMin

def check(xs: darray[i64]) -> i64:
    y: darray[i64] is SortedFirstMin[10] = xs
    return 0
`
	result := analyzeWithSMT(t, "smt_block_law.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis of the block-form law, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			proven++
		}
	}
	if proven != 1 {
		t.Fatalf("expected the block-form array-quantifier theorem proven by SMT, got %d: %+v", proven, result.ProofReport)
	}
}

// Soundness: a block-form law that is FALSE is still not proven — the inlining is semantics-preserving,
// not a way to sneak a proof past the solver. "all elements equal the first" does not follow.
func TestBlockFormLawDeclinesFalseClaim(t *testing.T) {
	src := `
law AllEqualFirst(self: darray[i64], n: i64):
    claim = forall i: (0 <= i and i < n) implies self[i] == self[0]
    return claim

def check(xs: darray[i64]) -> i64:
    y: darray[i64] is AllEqualFirst[10] = xs
    return 0
`
	result := analyzeWithSMT(t, "smt_block_law_false.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("a false block-form law must not be SMT-proven: %+v", result.ProofReport)
		}
	}
}

// Block-form bindings chain: a later binding may reference an earlier one, and the inline resolves in
// order. `both` references `lo` and `hi`. A scalar law proven by the cheap tiers (no SMT needed).
func TestBlockFormLawChainedBindings(t *testing.T) {
	src := `
law InUnit(self: i64):
    lo = self >= 0
    hi = self <= 1
    both = lo and hi
    return both

def pick() -> i64:
    x: i64 is InUnit = 1
    return x
`
	result := analyzeTreeTestSource(t, "block_law_chain.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("chained block-form law should analyze cleanly, got: %v", errs)
	}
}

// SMT TRIGGER TUNING (docs/90 brick 90-16): array-element quantifiers now carry an explicit
// `:pattern ((select arr i))` E-matching trigger. This test locks in that the trigger does not
// regress completeness — the sorted-implies-first-is-min theorem (which needs the sorted hypothesis
// instantiated at several indices) still proves — and that a purely arithmetic quantifier (no select
// term, so no pattern, left to MBQI) also still proves.
func TestSMTTriggerPreservesArrayAndArithmeticProofs(t *testing.T) {
	src := `
law SortedFirstMin(self: darray[i64], n: i64) = (forall i: (0 <= i and i < n - 1) implies self[i] <= self[i + 1]) implies (forall j: (0 <= j and j < n) implies self[0] <= self[j])
law NotDouble(self: i64, n: i64) = forall k: (0 <= k and k < n) implies self != k * 2

def check(xs: darray[i64]) -> i64:
    y: darray[i64] is SortedFirstMin[10] = xs
    return 0

def pick() -> i64:
    x: i64 is NotDouble[5] = 11
    return x
`
	result := analyzeWithSMT(t, "smt_triggers.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			proven++
		}
	}
	if proven != 2 {
		t.Fatalf("triggers must preserve both the array theorem and the arithmetic quantifier proof, got %d: %+v", proven, result.ProofReport)
	}
}

// With SMT off (default), the same nonlinear obligation is NOT proven and no solver runs.
func TestSMTOffLeavesNonlinearUnproven(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
type Small = i64 is Bounded[2, 10]

def mul(a: Small, b: Small) -> i64 is Bounded[4, 100]:
    return a * b
`
	result := analyzeTreeTestSource(t, "smt_off.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("SMT tier must not run when disabled: %+v", result.ProofReport)
		}
	}
	if result.SMTProfile.Enabled {
		t.Fatalf("SMT profile should be disabled by default, got %+v", result.SMTProfile)
	}
}

// docs/85 gap #2: the prover reasons THROUGH immutable local definitions and value-preserving
// (same-sign, widening) integer conversions. `rack.usize()*4096 + voice.usize()` over bounded
// u32 params proves its [0,131071] return bound DIRECTLY — no inner/outer usize restructuring.
func TestRefinementThroughLocalsAndWideningConversions(t *testing.T) {
	src := `
law Bounded(self: usize, lo: usize, hi: usize) = self >= lo and self <= hi

def to_slot(rack: u32 is Bounded[0, 31], voice: u32 is Bounded[0, 4095]) -> usize is Bounded[0, 131071]:
    return rack.usize() * 4096 + voice.usize()
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "through_conv.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("u32.usize() widening + bounded params should prove the return bound directly, got:\n%s", strings.Join(errs, "\n"))
	}
}

// A SIGN-changing conversion (i64 -> u64) is NOT value-preserving (it wraps for negatives), so it
// is correctly NOT treated as identity — soundness floor for the conversion-transparency rule.
func TestSignChangingConversionNotIdentity(t *testing.T) {
	src := `
def widen(x: i64) -> i64:
    ensure result == x
    r: i64 = x.u64().i64()
    return r
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "signchg.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("a sign-changing i64->u64->i64 round-trip must NOT be assumed identity, got:\n%s", allDiagnostics(r))
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
