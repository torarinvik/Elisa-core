//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

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

// A SIGN-changing conversion (i64 -> u64) wraps for negatives: codegen lowers a same-width integer
// conversion as a no-op (coerceNumericValue: actualBits == expectedBits returns the value), so the
// round trip x.u64().i64() IS the identity and proves; but the intermediate u64 is NOT x — a claim
// that only holds without the wrap must stay unproven (soundness floor for the reinterpret encoding).
func TestSignChangingConversionNotIdentity(t *testing.T) {
	src := `
def widen(x: i64) -> i64:
    ensure result == x
    r: i64 = x.u64().i64()
    return r

def nonneg(x: i64) -> i64:
    ensure result >= 0
    r: i64 = x.u64().i64()
    return r

def as_unsigned(x: i64) -> bool:
    ensure result
    return x.u64() <= 9223372036854775807
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "signchg.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	diags := allDiagnostics(r)
	if ensureFailuresFor(r.Errors(), "widen") != 0 {
		t.Fatalf("same-width round trip is the identity bit pattern and must prove, got:\n%s", diags)
	}
	for _, fn := range []string{"nonneg", "as_unsigned"} {
		if ensureFailuresFor(r.Errors(), fn) == 0 {
			t.Fatalf("%s: a negative x wraps through u64; the claim is false and must stay unproven, got:\n%s", fn, diags)
		}
	}
}

// Same-width signedness conversions preserve the underlying bits. The signed-to-unsigned and
// unsigned-to-signed pair therefore round-trips exactly, including negative signed inputs.
func TestSameWidthSignChangingConversionRoundTripIsIdentity(t *testing.T) {
	src := `
def widen(x: i64) -> i64:
    ensure result == x
    r: i64 = x.u64().i64()
    return r
def shrink(x: u64) -> u64:
    ensure result == x
    r: u64 = x.i64().u64()
    return r
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "signchg.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if len(r.Errors()) != 0 {
		t.Fatalf("same-width sign conversion round-trip should preserve the original value, got:\n%s", allDiagnostics(r))
	}
	if !proofReportContains(r.ProofReport, "ensure widen", "ensure", ProofProvenSMT) {
		t.Fatalf("expected the exact bit-preserving round-trip to prove, got: %+v", r.ProofReport)
	}
	if !proofReportContains(r.ProofReport, "ensure shrink", "ensure", ProofProvenSMT) {
		t.Fatalf("expected the reverse bit-preserving round-trip to prove, got: %+v", r.ProofReport)
	}
}

func TestSignedToUnsignedConversionDoesNotPreserveNegativeValue(t *testing.T) {
	src := `
def widen(x: i64) -> u64:
    ensure result <= 9223372036854775807
    return x.u64()
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "signed_to_unsigned.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("negative i64 values convert to u64 values above i64::MAX, so the postcondition must not be proven; got diagnostics:\n%s\nproof report: %+v", allDiagnostics(r), r.ProofReport)
	}
}
