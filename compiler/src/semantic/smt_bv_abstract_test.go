package semantic

import (
	"strings"
	"testing"
	"time"

	"elisacore/src/smt"
)

const maskAnd = "(bv2nat (bvand ((_ int2bv 64) h) ((_ int2bv 64) mask)))"

// The stage1 hash-table shape: slot = hash & (capacity - 1), goal slot < capacity. The integer
// core is trivial, but with the Int<->BV bridge present z3 exhausts a small budget; the
// abstraction (bv2nat read-backs as fresh Ints + the guarded AND bound lemma) proves it cheaply.
func TestSMTBitvectorAbstractionProvesMaskBoundCheaply(t *testing.T) {
	small := openCOISolver(t, 20000)
	goal := "(assert (not (< slot cap)))"
	query := "(declare-const h Int)\n(declare-const mask Int)\n(declare-const cap Int)\n(declare-const slot Int)\n" +
		"(assert (>= h 0))\n(assert (>= cap 1))\n(assert (< cap 18446744073709551616))\n" +
		"(assert (= mask (mod (- cap 1) 18446744073709551616)))\n" +
		smtAndBoundLemma(maskAnd, "h", "mask", 64) +
		"(assert (= slot " + maskAnd + "))\n" + goal + "\n"
	if res, _ := small.Check(smtStripAbstractionOnly(query)); res == smt.Unsat {
		t.Fatalf("precondition: the bridged query should not prove within 20k")
	}
	abstracted, ok := smtAbstractBitvectorReads(query)
	if !ok || strings.Contains(abstracted, "int2bv") {
		t.Fatalf("abstraction must remove every bit-vector term; ok=%v\n%s", ok, abstracted)
	}
	if res, _ := small.Check(abstracted); res != smt.Unsat {
		t.Fatalf("the abstraction proves slot < cap within 20k; got %s", res)
	}
	a := &Analyzer{}
	if res, _ := a.smtCheckSliced(small, query, goal, nil); res != smt.Unsat {
		t.Fatalf("smtCheckSliced must prove the mask bound; got %s", res)
	}
}

// budgetRecorder counts session-budget checks and records one-off budgets.
type budgetRecorder struct {
	smt.Solver
	budgets       []int64
	sessionChecks int
}

func (b *budgetRecorder) CheckValues(q string, vars []string) (smt.Result, map[string]string, time.Duration) {
	b.sessionChecks++
	return b.Solver.CheckValues(q, vars)
}

func (b *budgetRecorder) CheckBudget(q string, rlimit int64) smt.Result {
	res, _ := b.CheckBudgetValues(q, nil, rlimit)
	return res
}

func (b *budgetRecorder) CheckBudgetValues(q string, vars []string, rlimit int64) (smt.Result, map[string]string) {
	b.budgets = append(b.budgets, rlimit)
	return b.Solver.(smtBudgetedSolver).CheckBudgetValues(q, vars, rlimit)
}

// A FALSE goal over a masked value (`key_index < keys_len`, keys_len unconstrained) stays
// unproven, and once its abstraction is SAT the bridged query runs under the reduced budget
// instead of the session's 500k.
func TestSMTBitvectorFalseGoalDoesNotBurnBudget(t *testing.T) {
	spy := &budgetRecorder{Solver: openCOISolver(t, 500000)}
	goal := "(assert (not (< key_index keys_len)))"
	query := "(declare-const h Int)\n(declare-const mask Int)\n(declare-const key_index Int)\n(declare-const keys_len Int)\n" +
		"(assert (>= h 0))\n(assert (>= mask 0))\n(assert (< mask 18446744073709551616))\n(assert (>= keys_len 0))\n" +
		smtAndBoundLemma(maskAnd, "h", "mask", 64) +
		"(assert (= key_index " + maskAnd + "))\n" + goal + "\n"
	a := &Analyzer{}
	if res, _ := a.smtCheckSliced(spy, query, goal, nil); res == smt.Unsat {
		t.Fatal("key_index < keys_len is false (keys_len = 0) and must not prove")
	}
	if spy.sessionChecks != 1 || len(spy.budgets) != 1 || spy.budgets[0] != smtAfterSatAbstractionBudget {
		t.Fatalf("want 1 session-budget check (the abstraction) and the bridged query under %d; session=%d budgets=%v",
			smtAfterSatAbstractionBudget, spy.sessionChecks, spy.budgets)
	}
}

// The AND bound is guarded per operand: an operand term outside [0, 2^64) (here -1, whose bit
// pattern is all ones) gets no bound, so `(-1 & 5) <= -1` cannot be derived.
func TestSMTAndBoundLemmaGuardsNegativeOperands(t *testing.T) {
	solver := openCOISolver(t, 500000)
	and := "(bv2nat (bvand ((_ int2bv 64) a) ((_ int2bv 64) b)))"
	q := "(declare-const a Int)\n(declare-const b Int)\n(assert (= a (- 1)))\n(assert (= b 5))\n" +
		smtAndBoundLemma(and, "a", "b", 64)
	if res, _ := solver.Check(q); res != smt.Sat {
		t.Fatalf("the guarded lemma must be consistent with a negative operand; got %s", res)
	}
	// "Is (a & b) <= a derivable?" ⇔ is (not (<= R a)) unsat. It must not be.
	abstracted, ok := smtAbstractBitvectorReads(q + "(assert (not (<= " + and + " a)))\n")
	if !ok {
		t.Fatal("abstraction should apply")
	}
	if res, _ := solver.Check(abstracted); res != smt.Sat {
		t.Fatal("no bound may relate the AND to a negative operand: -1 & 5 = 5 > -1")
	}
}

// End to end: a signed `x & m <= m` is false (m = -1, x = 5 gives 5) and stays unproven, while the
// unsigned hash-mask bound proves.
func TestSMTMaskBoundEndToEnd(t *testing.T) {
	src := `
def slot_of(h: u64, cap: u64) -> u64:
    requires cap > 0
    ensure result < cap
    return h & (cap - 1)

def signed_mask(x: i64, m: i64) -> bool:
    ensure result
    return (x & m) <= m
`
	result := analyzeContractStrict(t, "mask_bound.elisa", src)
	if ensureFailuresFor(result.Errors(), "slot_of") != 0 {
		t.Fatalf("h & (cap - 1) < cap must prove; got %v", result.Errors())
	}
	if ensureFailuresFor(result.Errors(), "signed_mask") == 0 {
		t.Fatalf("signed x & m <= m is false for m < 0 and must stay unproven; got %v", result.Errors())
	}
}

// Every abstraction constant keeps bv2nat's range: 0 <= r always, and r < 2^W when the read-back
// is built from one int2bv width with width-preserving operators. A width-changing term (the
// signed read's sign-bit extract) keeps only the lower bound rather than a guessed width.
func TestSMTBitvectorAbstractionKeepsReadBackRange(t *testing.T) {
	q := "(declare-const h Int)\n(declare-const m Int)\n" +
		"(assert (= 1 " + maskAnd + "))\n" +
		"(assert (= 2 " + smtBitvectorRead("(bvand ((_ int2bv 32) h) ((_ int2bv 32) m))", true, 32) + "))\n"
	abstracted, ok := smtAbstractBitvectorReads(strings.ReplaceAll(q, "mask", "m"))
	if !ok {
		t.Fatalf("abstraction should apply:\n%s", q)
	}
	lower := strings.Count(abstracted, "(assert (<= 0 "+smtBVAbstractPrefix)
	if lower != 3 {
		t.Fatalf("every abstraction constant needs 0 <= r (want 3, got %d):\n%s", lower, abstracted)
	}
	if !strings.Contains(abstracted, " 18446744073709551616))") || !strings.Contains(abstracted, " 4294967296))") {
		t.Fatalf("64- and 32-bit read-backs need r < 2^W:\n%s", abstracted)
	}
	if strings.Count(abstracted, "(assert (< "+smtBVAbstractPrefix) != 2 {
		t.Fatalf("the extract (sign bit) read-back must not get a guessed upper bound:\n%s", abstracted)
	}
	if w, ok := smtBitvectorReadWidth("(bv2nat (bvand ((_ int2bv 64) a) ((_ int2bv 32) b)))"); ok {
		t.Fatalf("mixed widths must decline, got %d", w)
	}
}
