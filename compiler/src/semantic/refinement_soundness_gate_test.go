//go:build cgo

package semantic

// refinement_soundness_gate_test.go
//
// Soundness-gate suite for the refinement / where / requires / ensure pipeline.
// Purpose: assert invariants that must NEVER be violated for the system to be sound.
//
// Each test is labelled with the soundness invariant it guards.
// Tests that assert gaps not yet enforced are marked "// SOUNDNESS TODO" and skipped.
//
// Run with: go test ./src/semantic -run 'SoundnessGate'

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Invariant 1: Representation erasure
// `T where P` must be type-identical to `T` at the semantic boundary.
// SameType and AssignableTo must treat where-types as their base.
// ---------------------------------------------------------------------------

// SoundnessGate_WhereErasureSameType confirms that `i64 where P` and plain `i64`
// satisfy SameType — the predicate must not participate in structural type identity.
func TestSoundnessGate_WhereErasureSameType(t *testing.T) {
	src := `
def plain(n: i64) -> i64:
    return n

def refined(n: i64 where n >= 0) -> i64 where result >= 0:
    return n
`
	result := analyzeTreeTestSource(t, "soundness_erasure_sametype.elisa", src)

	plainSym, ok := result.GlobalScope.Lookup("plain")
	if !ok {
		t.Fatal("expected plain symbol")
	}
	refinedSym, ok := result.GlobalScope.Lookup("refined")
	if !ok {
		t.Fatal("expected refined symbol")
	}

	plain, ok := plainSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType for plain, got %T", plainSym.Type)
	}
	refined, ok := refinedSym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType for refined, got %T", refinedSym.Type)
	}

	// Param erasure
	if !SameType(plain.Params[0], refined.Params[0]) {
		t.Errorf("SOUNDNESS VIOLATION: param `i64 where n>=0` must erase to `i64` under SameType; got %s vs %s",
			plain.Params[0], refined.Params[0])
	}
	// Return erasure
	if !SameType(plain.Return, refined.Return) {
		t.Errorf("SOUNDNESS VIOLATION: return `i64 where result>=0` must erase to `i64` under SameType; got %s vs %s",
			plain.Return, refined.Return)
	}
}

// SoundnessGate_WhereErasureAssignableTo confirms bidirectional AssignableTo holds
// between `T where P` and `T` — the predicate must not introduce directional assignability.
func TestSoundnessGate_WhereErasureAssignableTo(t *testing.T) {
	src := `
def plain(n: i64) -> i64:
    return n

def refined(n: i64 where n > 0) -> i64 where result > 0:
    return n
`
	result := analyzeTreeTestSource(t, "soundness_erasure_assignable.elisa", src)

	plainSym, _ := result.GlobalScope.Lookup("plain")
	refinedSym, _ := result.GlobalScope.Lookup("refined")
	plain := plainSym.Type.(*FuncType)
	refined := refinedSym.Type.(*FuncType)

	if !AssignableTo(plain.Params[0], refined.Params[0]) {
		t.Errorf("SOUNDNESS VIOLATION: plain i64 must be assignable to i64 where P")
	}
	if !AssignableTo(refined.Params[0], plain.Params[0]) {
		t.Errorf("SOUNDNESS VIOLATION: i64 where P must be assignable to plain i64")
	}
	if !AssignableTo(plain.Return, refined.Return) {
		t.Errorf("SOUNDNESS VIOLATION: plain i64 return must be assignable to i64 where P return")
	}
	if !AssignableTo(refined.Return, plain.Return) {
		t.Errorf("SOUNDNESS VIOLATION: i64 where P return must be assignable to plain i64 return")
	}
}

// ---------------------------------------------------------------------------
// Invariant 2: Unprovable obligations are NOT silently dropped
// A where-predicate that cannot be statically discharged must produce a
// runtime-check obligation (ProofRuntime) or a diagnostic — not silence.
// ---------------------------------------------------------------------------

// SoundnessGate_UnprovableWhereProducesObligation asserts that calling a function
// whose param has `where P` with an argument that cannot be statically proven to
// satisfy P still produces an obligation (runtime check or diagnostic) rather than
// silently accepting the call.
func TestSoundnessGate_UnprovableWhereProducesObligation(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def caller(n: i64) -> i64:
    return need_positive(n)
`
	// Use AllowingDiagnostics so we can inspect even if it becomes an error.
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_unprovable_where.elisa", src, AnalyzeOptions{})

	// Either a ProofRuntime entry or a hard error/warning diagnostic must exist.
	hasRuntimeObligation := false
	for _, proof := range result.ProofReport {
		if proof.Predicate == "where" && proof.Outcome == ProofRuntime {
			hasRuntimeObligation = true
			break
		}
	}
	hasDiagnostic := strings.Contains(allDiagnostics(result), "where")

	if !hasRuntimeObligation && !hasDiagnostic {
		t.Errorf("SOUNDNESS VIOLATION: unprovable where-precondition for need_positive(n) "+
			"must produce a runtime obligation or diagnostic; proof report: %v", result.ProofReport)
	}
}

// ---------------------------------------------------------------------------
// Invariant 3: Refuted obligations are hard errors, not warnings
// If the prover can PROVE that a where predicate is violated, that must be
// an error (present in result.Errors()), not merely a warning or silenced.
// ---------------------------------------------------------------------------

// SoundnessGate_RefutedWhereIsError asserts that passing a constant that
// demonstrably violates a where-predicate is a semantic error.
func TestSoundnessGate_RefutedWhereIsError(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def bad() -> i64:
    return need_positive(0)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_refuted_where.elisa", src, AnalyzeOptions{})

	// A refuted where-obligation must appear as a hard error (not just a warning).
	hasRefutedProof := false
	for _, proof := range result.ProofReport {
		if proof.Predicate == "where" && proof.Outcome == ProofRefuted {
			hasRefutedProof = true
			break
		}
	}
	hasError := len(result.Errors()) > 0

	if !hasRefutedProof && !hasError {
		t.Errorf("SOUNDNESS VIOLATION: passing 0 to `x: i64 where x > 0` must produce "+
			"a ProofRefuted entry or a hard error; proof=%v errors=%v warnings=%v",
			result.ProofReport, result.Errors(), result.Warnings())
	}

	// If only a warning fires (not an error), that is itself a soundness gap.
	if hasRefutedProof && !hasError && len(result.Warnings()) > 0 {
		t.Errorf("SOUNDNESS VIOLATION: a refuted where-obligation must be a hard error, "+
			"not a warning; warnings=%v", result.Warnings())
	}
}

// ---------------------------------------------------------------------------
// Invariant 4: Bounds/index refinements — unprovable index still needs a check
// Erasing a runtime bounds check because of a refinement that is NOT statically
// proven is unsound. An unprovable index into an array must still produce an
// obligation (ProofRuntime or diagnostic).
// ---------------------------------------------------------------------------

// SoundnessGate_UnprovableIndexRetainsObligation asserts that an index
// whose range cannot be proven statically to fit inside the array still
// produces an "unchecked index" obligation or diagnostic.
func TestSoundnessGate_UnprovableIndexRetainsObligation(t *testing.T) {
	src := `
def read(idx: i64, arr: array[i64, 8]&) -> i64:
    return arr[idx]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_unprovable_index.elisa", src,
		AnalyzeOptions{EnforceUnsafePermissions: true})

	// With no refinement on idx the bounds check obligation must exist.
	diagStr := allDiagnostics(result)
	if !strings.Contains(diagStr, "unchecked index") && !strings.Contains(diagStr, "bounds") {
		// Also accept a runtime proof entry for an index obligation.
		hasRuntimeIndex := false
		for _, p := range result.ProofReport {
			if p.Outcome == ProofRuntime && (strings.Contains(p.Predicate, "index") || strings.Contains(p.Predicate, "bounds") || strings.Contains(p.Predicate, "InRange")) {
				hasRuntimeIndex = true
				break
			}
		}
		if !hasRuntimeIndex {
			t.Errorf("SOUNDNESS VIOLATION: unrefined idx into array[i64,8] must retain a "+
				"bounds-check obligation; diagnostics=%q proofs=%v", diagStr, result.ProofReport)
		}
	}
}

// SoundnessGate_TooWideRefinementRetainsBoundsCheck asserts that a refinement
// whose interval exceeds the array extent does NOT eliminate the bounds check.
// (Re-stated here at the soundness-gate level — already present in bounds_refinement_test.go
// but duplicated here so the gate suite is self-contained.)
func TestSoundnessGate_TooWideRefinementRetainsBoundsCheck(t *testing.T) {
	src := `
law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Inst:
    sel: mutable u32 is InRange[0, 255]
def read_field(d: Inst&, regs: array[u32, 128]&) -> u32:
    return regs[d.sel]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_wide_refinement_bounds.elisa", src,
		AnalyzeOptions{EnforceUnsafePermissions: true})

	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Errorf("SOUNDNESS VIOLATION: InRange[0,255] index into array[u32,128] must NOT "+
			"eliminate bounds check; diagnostics=%q", allDiagnostics(result))
	}
}

// ---------------------------------------------------------------------------
// Invariant 5: SMT-only proofs require SMT to be active
// An obligation that can ONLY be discharged by SMT must remain unproven
// (ProofRuntime, not ProofProvenSMT) when SMT is disabled.
// ---------------------------------------------------------------------------

// SoundnessGate_SMTOnlyObligationUnprovenWithoutSMT asserts that a
// non-linear where-predicate that requires SMT to prove is NOT marked
// proven when AnalyzeOptions.EnableSMT is false.
func TestSoundnessGate_SMTOnlyObligationUnprovenWithoutSMT(t *testing.T) {
	// This predicate requires SMT (nonlinear: a*b) — the affine tier can't prove it.
	src := `
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def pick(a: i64, b: i64) -> i64 is Bounded[4, 100]:
    requires a >= 2
    requires a <= 10
    requires b >= 2
    requires b <= 10
    return a * b
`
	// Run WITHOUT SMT.
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_smt_disabled.elisa", src, AnalyzeOptions{EnableSMT: false})

	// Without SMT the Bounded obligation must NOT be ProofProvenSMT.
	for _, p := range result.ProofReport {
		if p.Predicate == "Bounded" && p.Outcome == ProofProvenSMT {
			t.Errorf("SOUNDNESS VIOLATION: Bounded proof must not be ProofProvenSMT when "+
				"EnableSMT=false; proof=%+v", p)
		}
	}
}

// ---------------------------------------------------------------------------
// Invariant 6: `requires` preconditions are not silently elided
// A `requires` clause that cannot be verified at a call site must surface
// as a runtime check or diagnostic — never silently accepted.
// ---------------------------------------------------------------------------

// SoundnessGate_RequiresUnprovableNotSilent asserts that calling a function
// with a `requires` clause when the precondition cannot be verified at the
// call site does NOT silently pass.
func TestSoundnessGate_RequiresUnprovableNotSilent(t *testing.T) {
	src := `
def safe_div(a: i64, b: i64) -> i64:
    requires b != 0
    return a / b

def caller(x: i64, y: i64) -> i64:
    return safe_div(x, y)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_requires_unprovable.elisa", src, AnalyzeOptions{})

	hasObligation := false
	for _, p := range result.ProofReport {
		if (strings.Contains(p.Predicate, "requires") || strings.Contains(p.Predicate, "where")) &&
			(p.Outcome == ProofRuntime || p.Outcome == ProofRefuted) {
			hasObligation = true
			break
		}
	}
	hasDiagnostic := strings.Contains(allDiagnostics(result), "requires") ||
		strings.Contains(allDiagnostics(result), "precondition")

	if !hasObligation && !hasDiagnostic {
		t.Errorf("SOUNDNESS VIOLATION: unprovable `requires b != 0` at call site must "+
			"produce an obligation or diagnostic; proofs=%v diags=%q",
			result.ProofReport, allDiagnostics(result))
	}
}

// ---------------------------------------------------------------------------
// Invariant 7: Well-typed where-refinements analyze without spurious errors
// A valid well-formed where clause must not cause a hard error during analysis
// (the prover/eraser must not corrupt type checking).
// ---------------------------------------------------------------------------

// SoundnessGate_WellFormedWhereAnalyzesCleanly confirms that syntactically and
// semantically valid where-predicates do not produce spurious errors.
func TestSoundnessGate_WellFormedWhereAnalyzesCleanly(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"param_where", `
def f(n: i64 where n >= 0) -> i64:
    return n
`},
		{"return_where", `
def f(n: i64 where n >= 0) -> i64 where result >= 0:
    return n
`},
		{"local_where", `
def f() -> i64:
    x: i64 where x >= 0 = 5
    return x
`},
		{"is_law_type", `
law NonNeg(self: i64) = self >= 0
def f(n: i64 is NonNeg) -> i64:
    return n
`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeTreeTestSource(t, "soundness_wellformed_"+tc.name+".elisa", tc.src)
			if errs := result.Errors(); len(errs) != 0 {
				t.Errorf("SOUNDNESS VIOLATION: well-formed where clause produced spurious errors: %v", errs)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SOUNDNESS TODOs — gaps not yet enforced; skipped so they are visible
// ---------------------------------------------------------------------------

// A `ensure` postcondition that is PROVABLY refuted on a reachable return path
// must be a HARD ERROR (not a warning), independent of -strict. Closed by the
// ensure-refutation gate (analyzer_law_is.go: ensureClauseRefuted /
// reportRefutedEnsure). See ensure_refuted_soundness_test.go for the unit-level
// coverage; this is the gate-level guarantee.
func TestSoundnessGate_RefutedEnsureIsError(t *testing.T) {
	src := `
def f() -> i64:
    ensure result > 0
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_refuted_ensure.elisa", src, AnalyzeOptions{})

	if len(result.Errors()) == 0 {
		t.Fatalf("SOUNDNESS VIOLATION: returning 0 for `ensure result > 0` must be a hard error; "+
			"errors=%v warnings=%v proof=%v", result.Errors(), result.Warnings(), result.ProofReport)
	}
	refuted := false
	for _, p := range result.ProofReport {
		if p.Outcome == ProofRefuted && strings.HasPrefix(p.Subject, "ensure ") {
			refuted = true
			break
		}
	}
	if !refuted {
		t.Errorf("expected a ProofRefuted ensure entry; got %v", result.ProofReport)
	}
}

// A `where`-refined local REASSIGNED to a provably out-of-range value must be
// re-discharged at the assignment (refuted -> hard error), not silently carried
// forward on the stale fact. Closed by the reassignment re-discharge in
// analyzer_flow.go via whereTypeForReassignTarget + dischargeWhereRefinement.
func TestSoundnessGate_MutationInvalidatesWhereFactAtUse(t *testing.T) {
	src := `
def f() -> i64:
    x: i64 where x > 0 = 5
    x <- -1
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_where_reassign.elisa", src, AnalyzeOptions{})

	refuted := false
	for _, p := range result.ProofReport {
		if p.Predicate == "where" && p.Outcome == ProofRefuted {
			refuted = true
			break
		}
	}
	if !refuted && len(result.Errors()) == 0 {
		t.Errorf("SOUNDNESS VIOLATION: reassigning a `where x > 0` local to -1 must re-discharge "+
			"and refute; proof=%v errors=%v", result.ProofReport, result.Errors())
	}
}

// A callee's `where` precondition must be discharged at the call site even when
// it would only be visible via the canonical SpecSignature (the imported/
// cross-module case). A provably-violated precondition is a hard error; the
// SpecSignature-only path (no callee AST) is covered in
// crossmodule_where_soundness_test.go. This gate asserts the violated case is
// not silently accepted.
func TestSoundnessGate_CrossModuleWhereDischarge(t *testing.T) {
	src := `
def need_positive(x: i64 where x > 0) -> i64:
    return x

def bad() -> i64:
    return need_positive(-1)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_crossmodule_where.elisa", src, AnalyzeOptions{})

	refuted := false
	for _, p := range result.ProofReport {
		if p.Predicate == "where" && p.Outcome == ProofRefuted {
			refuted = true
			break
		}
	}
	if !refuted && len(result.Errors()) == 0 {
		t.Errorf("SOUNDNESS VIOLATION: a violated where-precondition must not be silently accepted; "+
			"proof=%v errors=%v", result.ProofReport, result.Errors())
	}
}
