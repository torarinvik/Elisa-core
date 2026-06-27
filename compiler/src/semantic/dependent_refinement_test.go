package semantic

import "testing"

func countRefinementProof(result *Result, lawName string, outcome ProofOutcome) int {
	n := 0
	for _, f := range result.ProofReport {
		if f.Predicate == lawName && f.Outcome == outcome {
			n++
		}
	}
	return n
}

// A VALUE-DEPENDENT refinement: the return is `Bounded[0, n]` where `n` is a runtime parameter, not a
// constant. The body returns `k`, and the preconditions `k >= 0 and k < n` entail the law — proven
// by the relational tier (tryProveRefinementByRelational) or SMT. The test accepts either.
func TestDependentRefinementReturnProvesBySMT(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self < hi

def pick(n: i64, k: i64) -> i64 is Bounded[0, n]:
    requires k >= 0
    requires k < n
    return k
`
	result := analyzeWithSMT(t, "dependent_bounded.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	// The relational tier now proves this before SMT; accept either ProofProvenLinear (relational) or ProofProvenSMT.
	smtProofs := countRefinementProof(result, "Bounded", ProofProvenSMT)
	linearProofs := countRefinementProof(result, "Bounded", ProofProvenLinear)
	if smtProofs+linearProofs < 1 {
		t.Fatalf("expected the value-dependent `Bounded[0, n]` return to be proven (SMT or relational linear), got smt=%d linear=%d: %+v", smtProofs, linearProofs, result.ProofReport)
	}
}

// The emulator `Index[cap]` shape: an unsigned slot index refined `InRange[0, cap]` where `cap` is a
// runtime bound, proven from `requires raw < cap` (unsignedness gives `raw >= 0` for free).
// The relational tier now proves this without SMT; the test accepts either tier.
func TestDependentRefinementIndexCapProvesBySMT(t *testing.T) {
	src := `
law InRange(self: usize, lo: usize, hi: usize) = self >= lo and self < hi

def slot(cap: usize, raw: usize) -> usize is InRange[0, cap]:
    requires raw < cap
    return raw
`
	result := analyzeWithSMT(t, "dependent_indexcap.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	// Relational tier proves this before SMT now; accept either.
	smtProofs := countRefinementProof(result, "InRange", ProofProvenSMT)
	linearProofs := countRefinementProof(result, "InRange", ProofProvenLinear)
	if smtProofs+linearProofs < 1 {
		t.Fatalf("expected the value-dependent `InRange[0, cap]` return to be proven (SMT or relational linear), got smt=%d linear=%d: %+v", smtProofs, linearProofs, result.ProofReport)
	}
}

// Soundness: when the upper bound is NOT established (only `k >= 0`, no `k < n`), the dependent
// refinement must NOT be proven — `k is Bounded[0, n]` can fail (take k = n), so SMT declines.
func TestDependentRefinementUnprovenDeclines(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self < hi

def pick_bad(n: i64, k: i64) -> i64 is Bounded[0, n]:
    requires k >= 0
    return k
`
	result := analyzeWithSMT(t, "dependent_bounded_unproven.elisa", src)
	if got := countRefinementProof(result, "Bounded", ProofProvenSMT); got != 0 {
		t.Fatalf("a dependent refinement without the upper bound must NOT be SMT-proven (k = n is a counterexample): %+v", result.ProofReport)
	}
}

// REPRODUCE: without the relational tier, a value-dependent refinement that SMT can prove fails
// under EnableSMT:false. This test documents the gap that tryProveRefinementByRelational closes.
// If this test already passes (no error under SMT-off), the tier is not needed for this shape.
func TestDependentRefinementIndexCapSMTOffReproducesGap(t *testing.T) {
	src := `
law InRange(self: usize, lo: usize, hi: usize) = self >= lo and self < hi

def slot(cap: usize, raw: usize) -> usize is InRange[0, cap]:
    requires raw < cap
    return raw
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dependent_gap_reproduce.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: false})
	if len(result.Errors()) == 0 {
		t.Logf("NOTE: the relational tier is already proving this shape without SMT — gap is closed")
	} else {
		t.Logf("REPRODUCED: without the relational tier, dependent refinement with non-const bracket arg fails under SMT-off: %v", result.Errors())
	}
}

// COMPLETENESS-POSITIVE: `raw is InRange[0, cap]` with `requires raw < cap` proves WITHOUT SMT via
// the new relational tier. The law body is `self >= 0 and self < cap`; `self >= 0` discharges from
// the declared usize type (non-negative by construction); `self < cap` discharges from `requires raw < cap`.
func TestDependentRefinementRelationalProvesWithoutSMT(t *testing.T) {
	src := `
law InRange(self: usize, lo: usize, hi: usize) = self >= lo and self < hi

def slot(cap: usize, raw: usize) -> usize is InRange[0, cap]:
    requires raw < cap
    return raw
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dependent_relational_proven.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: false})
	if len(result.Errors()) != 0 {
		t.Fatalf("relational tier should prove `raw is InRange[0, cap]` from `requires raw < cap` without SMT, got: %v", result.Errors())
	}
	if got := countRefinementProof(result, "InRange", ProofProvenLinear); got < 1 {
		t.Fatalf("expected at least one ProofProvenLinear for InRange (relational tier), got %d: %+v", got, result.ProofReport)
	}
}

// SOUNDNESS-NEGATIVE: `requires raw <= cap` (non-strict) does NOT prove `self < cap` (strict).
// `raw = cap` satisfies the precondition but violates `self < cap`, so the proof must be declined.
func TestDependentRefinementRelationalWeakerRequiresMustNotProve(t *testing.T) {
	src := `
law InRange(self: usize, lo: usize, hi: usize) = self >= lo and self < hi

def slot_bad(cap: usize, raw: usize) -> usize is InRange[0, cap]:
    requires raw <= cap
    return raw
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dependent_relational_unsound.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: false})
	if got := countRefinementProof(result, "InRange", ProofProvenLinear); got > 0 {
		t.Fatalf("SOUNDNESS FAILURE: `requires raw <= cap` must NOT prove `raw is InRange[0, cap]` (raw=cap is a counterexample): %+v", result.ProofReport)
	}
	// The result may have an error (strict proof required) or no error (runtime check inserted) —
	// both are acceptable. What is NOT acceptable is a ProofProvenLinear discharge.
}

// Dependence-freeze guardrail: a flow fact with a dependent bound (`i is Bounded[0, xs.count]`)
// is only live while the bound root is unchanged. Mutating `xs` drops the fact, so a later use of
// `i` against the same law must fall back to a runtime check instead of reusing stale proof data.
func TestDependentRefinementFactInvalidatedByContainerMutation(t *testing.T) {
	src := `
law Bounded(self: usize, lo: usize, hi: usize) = self >= lo and self < hi

def use_i(i: usize is Bounded[0, xs.count], xs: darray[u8]&) -> usize:
    return i

def f(owner: mutable Arena&, xs: mutable darray[u8], i: usize) -> usize:
    if i is Bounded[0, xs.count]:
        in owner:
            xs.truncate(0)
        return use_i(i, xs)
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dependent_refine_mutation_invalidates.elisa", src, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("stale dependent refinement fact should degrade to a boundary check, not a type error, got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("mutation of xs must invalidate the dependent Bounded[0,xs.count] fact; expected a call-arg runtime check")
	}
	if got := countRefinementProof(result, "Bounded", ProofProvenFlow); got > 1 {
		t.Fatalf("the original narrowing may prove once, but the post-mutation call arg must not be flow-proven again: %+v", result.ProofReport)
	}
}
