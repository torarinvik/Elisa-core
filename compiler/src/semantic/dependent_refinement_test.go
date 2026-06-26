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
// by SMT binding the law param `hi` to the term for `n`.
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
	if got := countRefinementProof(result, "Bounded", ProofProvenSMT); got != 1 {
		t.Fatalf("expected the value-dependent `Bounded[0, n]` return to be proven by SMT, got %d: %+v", got, result.ProofReport)
	}
}

// The emulator `Index[cap]` shape: an unsigned slot index refined `InRange[0, cap]` where `cap` is a
// runtime bound, proven from `requires raw < cap` (unsignedness gives `raw >= 0` for free).
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
	if got := countRefinementProof(result, "InRange", ProofProvenSMT); got != 1 {
		t.Fatalf("expected the value-dependent `InRange[0, cap]` return to be proven by SMT, got %d: %+v", got, result.ProofReport)
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
