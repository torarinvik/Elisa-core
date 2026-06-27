//go:build cgo

package semantic

import "testing"

// HYPOTHESIS: `requires a implies b` is a boolean implication that desugars to `(not a) or b` at
// parse time (TOKEN_OR with TOKEN_NOT left). The SMT tier must (1) assert it as a hypothesis when
// proving obligations in the same function, and (2) discharge it as an obligation at call sites.

// ── CASE 1: requires as hypothesis ──────────────────────────────────────────────────────────────

// A function with `requires x > 0 implies y > 0` and `requires x > 0` can prove `y > 0` from
// those two hypotheses via modus ponens. The SMT tier must translate both as assertions and close
// the return-type refinement.
func TestSMTBoolImpliesRequiresAsHypothesis(t *testing.T) {
	src := `
law Pos(self: i64) = self > 0

def f(x: i64, y: i64) -> i64 is Pos:
    requires x > 0 implies y > 0
    requires x > 0
    return y
`
	result := analyzeWithSMT(t, "smt_bool_implies_hyp.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("implication + antecedent should prove consequent via SMT; got errors: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			proven++
		}
	}
	if proven == 0 {
		t.Fatalf("expected the return refinement discharged from the implication hypothesis, got 0: %+v", result.ProofReport)
	}
}

// ── CASE 2: requires (not a or b) as hypothesis ─────────────────────────────────────────────────

// Same scenario written as explicit disjunction `not x > 0 or y > 0` — should behave identically
// since `a implies b` desugars to exactly `(not a) or b`.
func TestSMTBoolDisjunctionRequiresAsHypothesis(t *testing.T) {
	src := `
law Pos(self: i64) = self > 0

def f(x: i64, y: i64) -> i64 is Pos:
    requires (not x > 0) or y > 0
    requires x > 0
    return y
`
	result := analyzeWithSMT(t, "smt_bool_disj_hyp.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("disjunction + antecedent should prove consequent via SMT; got errors: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			proven++
		}
	}
	if proven == 0 {
		t.Fatalf("expected the return refinement discharged from the disjunction hypothesis, got 0: %+v", result.ProofReport)
	}
}

// ── CASE 3: requires implication as obligation at call site ─────────────────────────────────────

// When a callee declares `requires x > 0 implies y > 0`, a caller must establish that implication.
// A caller that itself has the same requires can prove it.
func TestSMTBoolImpliesRequiresAsObligation(t *testing.T) {
	src := `
def callee(x: i64, y: i64) -> i64:
    requires x > 0 implies y > 0
    return y

def caller(a: i64, b: i64) -> i64:
    requires a > 0 implies b > 0
    return callee(a, b)
`
	result := analyzeWithSMT(t, "smt_bool_implies_oblig.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("caller with matching implication should prove callee's implication requires; got: %v", errs)
	}
	var proven int
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT && f.Predicate == "requires" {
			proven++
		}
	}
	if proven == 0 {
		t.Fatalf("expected the callee's implication requires proven at call site, got 0: %+v", result.ProofReport)
	}
}

// ── SOUNDNESS NEGATIVE: obligation NOT entailed by hypotheses ───────────────────────────────────

// A function with `requires x > 0 implies y > 0` but WITHOUT `requires x > 0` cannot prove
// `ensures y > 0`: x might be <= 0, making the implication vacuously true, while y <= 0.
// The SMT tier MUST NOT prove this.
func TestSMTBoolImpliesRequiresHypothesisNegative(t *testing.T) {
	src := `
law Pos(self: i64) = self > 0

def f(x: i64, y: i64) -> i64 is Pos:
    requires x > 0 implies y > 0
    return y
`
	result := analyzeWithSMT(t, "smt_bool_implies_neg.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT {
			t.Fatalf("without antecedent, implication does not prove consequent — must not be SMT-proven: %+v", result.ProofReport)
		}
	}
}

// ── SOUNDNESS NEGATIVE: caller with wrong implication cannot prove callee ───────────────────────

// A callee requires `x > 0 implies y > 0`. A caller that only has `x > 5 implies y > 0` (not the
// same antecedent as `x > 0`) does NOT have a matching precondition and must not be proven.
func TestSMTBoolImpliesRequiresObligationNegative(t *testing.T) {
	src := `
def callee(x: i64, y: i64) -> i64:
    requires x > 0 implies y > 0
    return y

def caller(a: i64, b: i64) -> i64:
    requires a > 5 implies b > 0
    return callee(a, b)
`
	result := analyzeWithSMT(t, "smt_bool_implies_obl_neg.elisa", src)
	for _, f := range result.ProofReport {
		if f.Outcome == ProofProvenSMT && f.Predicate == "requires" {
			t.Fatalf("caller's weaker implication should not prove callee's stronger implication requires: %+v", result.ProofReport)
		}
	}
}
