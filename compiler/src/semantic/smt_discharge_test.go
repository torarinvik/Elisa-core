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
    return n / 2u
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

// Soundness gate: a SIGNED dividend that could be negative is NOT translated (Euclidean div would
// mismatch truncating div), so the obligation declines rather than risk an unsound proof.
func TestSMTDeclinesSignedDivision(t *testing.T) {
	src := `
law NonNeg(self: i64) = self >= 0

def half(n: i64) -> i64 is NonNeg:
    return n / 2
`
	result := analyzeWithSMT(t, "smt_signed_div.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("signed division must not be SMT-proven (could be negative): %+v", result.ProofReport)
		}
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
