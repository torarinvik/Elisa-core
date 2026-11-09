//go:build cgo

package semantic

import (
	"testing"
)

// After `clamp(&n)` with `ensures n is Bounded[0, 9]`, the caller knows n ∈ [0, 9]. A subsequent
// obligation for the WIDER `Bounded[0, 20]` proves only via interval entailment ([0,9] ⊆ [0,20]) —
// the factset holds the exact key Bounded[0,9], never Bounded[0,20], so this is the interval seed at
// work, not the predicate-fact gain.
func TestEnsuresParamSeedsCallerInterval(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def clamp(p: mutable i64&) -> void ensures p is Bounded[0, 9]:
    p <- 0

def use(seed: i64) -> i64:
    n: mutable i64 = seed
    clamp(&n)
    m: i64 is Bounded[0, 20] = n
    return m
`
	result := analyzeTreeTestSource(t, "ensures_param_interval_seed.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`ensures n is Bounded[0,9]` should let the WIDER `Bounded[0,20]` prove via interval, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("interval [0,9] entails Bounded[0,20]: expected static proof, got %d runtime checks", len(result.RefinementChecks))
	}
}

// Soundness floor for brick 90-11: mutating the variable AFTER the call drops the seeded interval
// (invalidateRangeFactsForTarget), so the same obligation falls back to a runtime check.
func TestEnsuresParamIntervalDroppedOnMutation(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def clamp(p: mutable i64&) -> void ensures p is Bounded[0, 9]:
    p <- 0

def use(seed: i64) -> i64:
    n: mutable i64 = seed
    clamp(&n)
    n <- seed
    m: i64 is Bounded[0, 20] = n
    return m
`
	result := analyzeTreeTestSource(t, "ensures_param_interval_drop.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected analysis to succeed with a runtime fallback, got: %v", errs)
	}
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("re-mutating n must drop the seeded interval, forcing a runtime check")
	}
}

// Without the `ensures`, the same ref call DROPS the fact (brick 1) — confirms the postcondition is
// what carries the guarantee across the call.
func TestEnsuresRefinementGainIsLoadBearing(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def bump(p: mutable i64&) -> void:
    p <- 1

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Positive:
        bump(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "ensures_loadbearing.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("a ref call WITHOUT `ensures` must drop the fact, forcing a runtime check")
	}
}

// A refuted postcondition (`ensures p is Positive` but the body provably sets it non-positive) is a
// compile error from the static half.
func TestEnsuresRefinementRefutedStatically(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def bad(p: mutable i64&) -> void ensures p is Positive:
    p <- 0
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_refute.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("`p <- 0` violates the Positive postcondition: expected a compile-time refutation, got:\n%s", allDiagnostics(result))
	}
}

// `ensures` referencing an unknown parameter, or a non-law, is rejected.
func TestEnsuresRefinementValidation(t *testing.T) {
	bad := `
law Positive(self: i64) = self > 0
def f(p: mutable i64&) -> void ensures q is Positive:
    p <- 1
`
	r1 := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_unknown_param.elisa", bad, AnalyzeOptions{})
	if !contains(allDiagnostics(r1), "unknown parameter") {
		t.Fatalf("ensures on unknown param should be rejected, got:\n%s", allDiagnostics(r1))
	}
	notLaw := `
def f(p: mutable i64&) -> void ensures p is Nope:
    p <- 1
`
	r2 := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_notlaw.elisa", notLaw, AnalyzeOptions{})
	if !contains(allDiagnostics(r2), "is not a law") {
		t.Fatalf("ensures with a non-law predicate should be rejected, got:\n%s", allDiagnostics(r2))
	}
}

// Written-constant tracking through a ref pointee PROVES a postcondition: `p <- 1` makes the
// `ensures p is Positive` provable at the return (no warning), and the call-site gain follows.
func TestEnsuresRefinementProvenByWrittenConst(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def set_pos(p: mutable i64&) -> void ensures p is Positive:
    p <- 1
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_proven_write.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`p <- 1` should statically prove `ensures p is Positive` (clean under -strict), got: %v", result.Errors())
	}
	for _, f := range result.ProofReport {
		if f.Predicate == "Positive" && f.Outcome == ProofRuntime {
			t.Fatalf("postcondition should be proven (not runtime) after `p <- 1`, report=%+v", result.ProofReport)
		}
	}
}

// A non-constant write (`p <- v`) cannot be proven — reported as unproven (warning; -strict error).
func TestEnsuresRefinementUnprovenNonConstWrite(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def mk(p: mutable i64&, v: i64) -> void ensures p is Positive:
    p <- v
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_unproven.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "could not be proven statically") {
		t.Fatalf("a non-constant write should leave the postcondition unproven (warned), got:\n%s", allDiagnostics(result))
	}
	strict := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_unproven_strict.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(strict.Errors()) == 0 {
		t.Fatalf("under -strict an unprovable postcondition should be a hard error")
	}
}

// --- Mutable refinement flow brick 3: preserve-credit. A ref call the callee provably doesn't use
// to mutate the argument KEEPS the caller's refinement facts. ---

// An immutable-borrow call (`p: i64&`) cannot mutate the argument, so a narrowed fact survives it —
// the obligation after the call still proves statically (no runtime check).
func TestPreserveCreditImmutableBorrow(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def observe(p: i64&) -> void:
    pass

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Positive:
        observe(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "preserve_immutable_borrow.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("immutable-borrow call should preserve the Positive fact, got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("fact should survive an immutable-borrow call (no runtime check), got %d", len(result.CallArgRefinementChecks))
	}
}

// SOUNDNESS GUARD: the canonical mutable borrow `p: mutable i64&` CAN mutate, so the fact is still
// dropped (a runtime check is forced) — preserve-credit must not over-apply.
func TestPreserveCreditMutableBorrowStillDrops(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def touch(p: mutable i64&) -> void:
    pass

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Positive:
        touch(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "preserve_mutable_borrow_drops.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("a mutable-borrow call must still DROP the fact (preserve-credit must not over-apply)")
	}
}

// An explicit `ensures p => preserve` postcondition grants preserve-credit even for a mutable
// borrow: the callee guarantees the argument is unchanged, so the fact survives.
func TestPreserveCreditExplicitEnsuresPreserve(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def needs_pos(x: i64 is Positive) -> i64:
    return x

def keep(p: mutable i64&) -> void ensures p => preserve:
    pass

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Positive:
        keep(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "preserve_explicit_ensures.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`ensures p => preserve` should preserve the fact, got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("explicit preserve should keep the fact (no runtime check), got %d", len(result.CallArgRefinementChecks))
	}
}

// Preserve-credit now reads the immutable-borrow signal from the resolved FuncType, so it applies to
// METHOD calls too (not just direct by-name calls): a method with an immutable-borrow param `p: i64&`
// cannot mutate the argument, so the caller's narrowed fact survives `recv.observe(&n)`.
func TestPreserveCreditImmutableBorrowMethodCall(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

struct Logger:
    count: i64

impl Logger:
    def observe(self: Logger, p: i64&) -> void:
        pass

def needs_pos(x: i64 is Positive) -> i64:
    return x

def f(seed: i64) -> i64:
    lg: mutable Logger = zeroed
    n: mutable i64 = seed
    if n is Positive:
        lg.observe(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "preserve_immutable_borrow_method.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("immutable-borrow METHOD call should preserve the Positive fact, got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("fact should survive an immutable-borrow method call (no runtime check), got %d", len(result.CallArgRefinementChecks))
	}
}

// SOUNDNESS GUARD: a method with a MUTABLE-borrow param still DROPS the fact through a method call.
func TestPreserveCreditMutableBorrowMethodStillDrops(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

struct Logger:
    count: i64

impl Logger:
    def touch(self: Logger, p: mutable i64&) -> void:
        pass

def needs_pos(x: i64 is Positive) -> i64:
    return x

def f(seed: i64) -> i64:
    lg: mutable Logger = zeroed
    n: mutable i64 = seed
    if n is Positive:
        lg.touch(&n)
        return needs_pos(n)
    return 0
`
	result := analyzeTreeTestSource(t, "preserve_mutable_borrow_method_drops.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("a mutable-borrow method call must still DROP the fact (preserve-credit must not over-apply)")
	}
}

// --- Mutable refinement flow brick 4: parametric facts. `if x is Bounded[0,500]:` carries a fact
// keyed by the constant bounds, usable across mutations, with exact-match (no over-proving). ---

// A parametric fact rides a MUTABLE variable: `if n is Bounded[0,500]:` proves a later
// `Bounded[0,500]` obligation on n with no runtime check.
func TestParametricMutableFactProves(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def needs_b(x: i64 is Bounded[0, 500]) -> i64:
    return x

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Bounded[0, 500]:
        return needs_b(n)
    return 0
`
	result := analyzeTreeTestSource(t, "param_mut_fact_proves.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("parametric fact on mutable n should prove needs_b(n), got: %v", errs)
	}
	if len(result.CallArgRefinementChecks) != 0 {
		t.Fatalf("parametric mutable fact should prove statically, got %d runtime checks", len(result.CallArgRefinementChecks))
	}
}

// SOUNDNESS: a parametric fact discharges only the SAME bounds — `Bounded[0,500]` must NOT prove a
// `Bounded[10,20]` obligation (exact key match, no cross-bounds entailment).
func TestParametricMutableFactExactMatchOnly(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def needs_tight(x: i64 is Bounded[10, 20]) -> i64:
    return x

def f(seed: i64) -> i64:
    n: mutable i64 = seed
    if n is Bounded[0, 500]:
        return needs_tight(n)
    return 0
`
	result := analyzeTreeTestSource(t, "param_mut_fact_exact.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("`Bounded[0,500]` must NOT prove the tighter `Bounded[10,20]` — a runtime check is required")
	}
}

// A mutation drops a parametric fact just like a bare one.
func TestParametricMutableFactDroppedByMutation(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def needs_b(x: i64 is Bounded[0, 500]) -> i64:
    return x

def f(seed: i64, other: i64) -> i64:
    n: mutable i64 = seed
    if n is Bounded[0, 500]:
        n <- other
        return needs_b(n)
    return 0
`
	result := analyzeTreeTestSource(t, "param_mut_fact_drop.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("mutation `n <- other` must drop the parametric fact, forcing a runtime check")
	}
}

// --- Mutable refinement flow: dependent length facts (docs/85 §5.3 dependence-freeze). A fact whose
// bounds read a frozen variable (`i is Bounded[0, xs.count]`) discharges a same-scope obligation and
// is dropped when either the holder OR a dependency mutates. ---

// A dependent fact `i is Bounded[0, xs.count]` proves a same-scope obligation with the identical
// dependent bound — no runtime check — while xs is frozen.
func TestDependentLengthFactProves(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def f(xs: darray[i64], seed: i64) -> void:
    i: mutable i64 = seed
    if i is Bounded[0, xs.count]:
        j: i64 is Bounded[0, xs.count] = i
    return
`
	result := analyzeTreeTestSource(t, "dep_len_proves.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("dependent fact should prove the same-bound obligation, got: %v", errs)
	}
	if len(result.RefinementChecks) != 0 {
		t.Fatalf("dependent fact should prove statically (no runtime check), got %d", len(result.RefinementChecks))
	}
}

// The exclusive sugar `Bounded[0..<xs.count]` parses as a dependent param refinement (docs/85 §5.3),
// desugaring to `Bounded[0, xs.count - 1]` — the doc's canonical dependent-bound form.
func TestDependentLengthExclusiveSugarParses(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def f(xs: darray[i64], i: i64 is Bounded[0..<xs.count]) -> void:
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dep_len_exclusive.elisa", src, AnalyzeOptions{})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`Bounded[0..<xs.count]` dependent param refinement should parse cleanly, got: %v", errs)
	}
}

// SOUNDNESS (freeze): mutating the DEPENDENCY drops the dependent fact even though the holder `i` was
// untouched — a stale bound must not discharge. (Scalar dependency `n` keeps the test arena-free.)
func TestDependentLengthFactDroppedByDependencyMutation(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def f(n: mutable i64, seed: i64, other: i64) -> void:
    i: mutable i64 = seed
    if i is Bounded[0, n]:
        n <- other
        j: i64 is Bounded[0, n] = i
    return
`
	result := analyzeTreeTestSource(t, "dep_len_dep_mutated.elisa", src)
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("mutating the dependency `n` must drop the dependent fact, forcing a runtime check")
	}
}

// SOUNDNESS (holder): mutating the holder `i` also drops the dependent fact, like any pred fact.
func TestDependentLengthFactDroppedByHolderMutation(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def f(xs: darray[i64], seed: i64, other: i64) -> void:
    i: mutable i64 = seed
    if i is Bounded[0, xs.count]:
        i <- other
        j: i64 is Bounded[0, xs.count] = i
    return
`
	result := analyzeTreeTestSource(t, "dep_len_holder_mutated.elisa", src)
	if len(result.RefinementChecks) == 0 {
		t.Fatalf("mutating the holder `i` must drop the dependent fact, forcing a runtime check")
	}
}

// SOUNDNESS (cross-function): a caller's dependent fact must NOT discharge a callee argument
// obligation by mere name coincidence — the callee's `xs` is a different variable.
func TestDependentLengthFactNotMatchedAcrossCall(t *testing.T) {
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def needs_idx(xs: darray[i64], x: i64 is Bounded[0, xs.count]) -> void:
    return

def f(xs: darray[i64], seed: i64) -> void:
    i: mutable i64 = seed
    if i is Bounded[0, xs.count]:
        needs_idx(xs, i)
    return
`
	result := analyzeTreeTestSource(t, "dep_len_cross_call.elisa", src)
	if len(result.CallArgRefinementChecks) == 0 {
		t.Fatalf("a dependent fact must NOT discharge across a call boundary by name coincidence; a runtime check is required")
	}
}

// --- Mutable refinement flow: non-int written-const. The written-constant channel tracks any
// const-evaluable kind (bool, enum), not just integers, through ref pointees and plain writes. ---

// A BOOL written constant proves a bool law: `ready <- true` makes `ensures ready is On` provable.
func TestEnsuresRefinementProvenByWrittenBool(t *testing.T) {
	src := `
law On(self: bool) = self
def arm(p: mutable bool&) -> void ensures p is On:
    p <- true
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_bool_proven.elisa", src, AnalyzeOptions{EnforceStrictProofs: true})
	if len(result.Errors()) != 0 {
		t.Fatalf("`p <- true` should statically prove `ensures p is On`, got: %v", result.Errors())
	}
}

// A BOOL written constant that violates the law is refuted (compile error), not merely warned.
func TestEnsuresRefinementRefutedByWrittenBool(t *testing.T) {
	src := `
law On(self: bool) = self
def disarm(p: mutable bool&) -> void ensures p is On:
    p <- false
    return
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ensures_bool_refuted.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is violated") {
		t.Fatalf("`p <- false` should REFUTE `ensures p is On` (not just warn), got:\n%s", allDiagnostics(result))
	}
}
