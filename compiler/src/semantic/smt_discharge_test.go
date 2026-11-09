//go:build cgo

package semantic

import (
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"os/exec"
	"strings"
	"testing"
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
