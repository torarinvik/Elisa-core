//go:build cgo

package semantic

import (
	"os/exec"
	"strings"
	"testing"

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
	if !result.SMTProfile.Enabled || result.SMTProfile.Proven != 1 {
		t.Fatalf("expected SMT profile to record 1 proven, got %+v", result.SMTProfile)
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
	if result.SMTProfile.Proven != 0 || result.SMTProfile.Declined < 1 {
		t.Fatalf("expected SMT to attempt and decline the false bound, got %+v", result.SMTProfile)
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
