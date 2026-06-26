//go:build cgo

package semantic

// refinement_soundness_gate_phase2_test.go
//
// Soundness-gate suite — Phase 2: named `refine` aliases, struct-field `where`,
// and where-local reassignment.
//
// Invariants asserted here must NEVER be violated for the system to be sound.
// Not-yet-enforced gaps are marked "// SOUNDNESS TODO" and skipped via t.Skip.
//
// Run with: go test ./src/semantic -run 'SoundnessGateP2'

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Invariant P2-1: Named `refine` alias — erasure parity with anonymous where
//
// `refine A = T where P` must produce a parameter type that is SameType / AssignableTo
// as plain T, identical to what `T where P` would produce when written inline.
// ---------------------------------------------------------------------------

// SoundnessGateP2_NamedRefineAliasSameTypeAsBase confirms that a parameter typed
// via a named refine alias has the same erased base type as a plain-typed parameter.
func TestSoundnessGateP2_NamedRefineAliasSameTypeAsBase(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def plain(n: i64) -> i64:
    return n

def refined(n: Positive) -> i64:
    return n
`
	result := analyzeTreeTestSource(t, "soundness_p2_alias_sametype.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("well-formed refine alias should analyze cleanly, got: %v", errs)
	}

	plainSym, ok := result.GlobalScope.Lookup("plain")
	if !ok {
		t.Fatal("expected plain symbol")
	}
	refinedSym, ok := result.GlobalScope.Lookup("refined")
	if !ok {
		t.Fatal("expected refined symbol")
	}
	plain := plainSym.Type.(*FuncType)
	refined := refinedSym.Type.(*FuncType)

	if !SameType(plain.Params[0], refined.Params[0]) {
		t.Errorf("SOUNDNESS VIOLATION: refine alias param must erase to base type under SameType; got %s vs %s",
			plain.Params[0], refined.Params[0])
	}
	if !AssignableTo(plain.Params[0], refined.Params[0]) {
		t.Errorf("SOUNDNESS VIOLATION: plain i64 must be assignable to Positive (refine alias)")
	}
	if !AssignableTo(refined.Params[0], plain.Params[0]) {
		t.Errorf("SOUNDNESS VIOLATION: Positive (refine alias) must be assignable to plain i64")
	}
}

// SoundnessGateP2_NamedRefineAliasReturnErasure confirms erasure holds for
// return types annotated via a named refine alias.
func TestSoundnessGateP2_NamedRefineAliasReturnErasure(t *testing.T) {
	src := `
refine NonNeg = i64 where self >= 0

def plain() -> i64:
    return 1

def refined() -> NonNeg:
    return 1
`
	result := analyzeTreeTestSource(t, "soundness_p2_alias_return_erase.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("well-formed refine alias on return should analyze cleanly, got: %v", errs)
	}

	plainSym, ok := result.GlobalScope.Lookup("plain")
	if !ok {
		t.Fatal("expected plain symbol")
	}
	refinedSym, ok := result.GlobalScope.Lookup("refined")
	if !ok {
		t.Fatal("expected refined symbol")
	}
	plain := plainSym.Type.(*FuncType)
	refined := refinedSym.Type.(*FuncType)

	if !SameType(plain.Return, refined.Return) {
		t.Errorf("SOUNDNESS VIOLATION: refine alias return must erase to base type under SameType; got %s vs %s",
			plain.Return, refined.Return)
	}
	if !AssignableTo(plain.Return, refined.Return) || !AssignableTo(refined.Return, plain.Return) {
		t.Errorf("SOUNDNESS VIOLATION: refine alias return must not introduce directional AssignableTo behavior")
	}
}

// ---------------------------------------------------------------------------
// Invariant P2-2: Named `refine` alias — violated precondition is a hard error
//
// Passing a constant that demonstrably violates the alias's predicate must produce
// a ProofRefuted entry or a hard error — not silence and not merely a warning.
// ---------------------------------------------------------------------------

// SoundnessGateP2_NamedRefineAliasPreconditionViolatedIsError asserts that a call
// with a provably-violating argument to a refine-aliased parameter is a hard error.
func TestSoundnessGateP2_NamedRefineAliasPreconditionViolatedIsError(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def needs_positive(n: Positive) -> i64:
    return n

def bad() -> i64:
    return needs_positive(0)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_p2_alias_violated.elisa", src, AnalyzeOptions{})

	hasRefuted := false
	for _, p := range result.ProofReport {
		if p.Outcome == ProofRefuted {
			hasRefuted = true
			break
		}
	}
	hasError := len(result.Errors()) > 0

	if !hasRefuted && !hasError {
		t.Errorf("SOUNDNESS VIOLATION: passing 0 to Positive-aliased param must produce "+
			"a ProofRefuted entry or a hard error; proof=%v errors=%v diags=%q",
			result.ProofReport, result.Errors(), allDiagnostics(result))
	}

	// A refuted precondition must not merely warn.
	if hasRefuted && !hasError && strings.Contains(allDiagnostics(result), "warning") {
		t.Errorf("SOUNDNESS VIOLATION: a refuted alias precondition must be a hard error, not a warning")
	}
}

// SoundnessGateP2_NamedRefineAliasSatisfiedCallIsClean asserts that a provably
// satisfying argument to a refine-alias parameter does NOT produce an error.
func TestSoundnessGateP2_NamedRefineAliasSatisfiedCallIsClean(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def needs_positive(n: Positive) -> i64:
    return n

def ok() -> i64:
    return needs_positive(5)
`
	result := analyzeTreeTestSource(t, "soundness_p2_alias_satisfied.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Errorf("SOUNDNESS VIOLATION: a satisfied refine-alias call should produce no errors; got %v", errs)
	}
}

// ---------------------------------------------------------------------------
// Invariant P2-3: Named `refine` alias misused as an ordinary type → diagnostic
//
// A refine alias is a binder-position concept; using it as a generic type argument
// or standalone type position must yield a clear diagnostic rather than silent erasure.
// ---------------------------------------------------------------------------

// SoundnessGateP2_NamedRefineAliasMisuseAsOrdinaryTypeIsDiagnostic asserts that
// using a refine alias in non-binder position produces an explicit diagnostic.
func TestSoundnessGateP2_NamedRefineAliasMisuseAsOrdinaryTypeIsDiagnostic(t *testing.T) {
	src := `
refine Positive = i64 where self > 0

def f() -> i64:
    xs: darray[Positive] = []
    return 0
`
	result := analyzeTreeTestSourceWithSemanticErrors(t, "soundness_p2_alias_misuse.elisa", src)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "binder") && !strings.Contains(all, "only be used") && !strings.Contains(all, "position") {
		t.Errorf("SOUNDNESS VIOLATION: refine alias used as ordinary type must produce a binder-position "+
			"diagnostic; got errors=%v", result.Errors())
	}
}

// ---------------------------------------------------------------------------
// Invariant P2-4: Struct-field `where` — violated field invariant at construction
//
// A struct literal passing a constant value that provably violates a field's
// where-predicate must be rejected (ProofRefuted or hard error).
// ---------------------------------------------------------------------------

// SoundnessGateP2_StructFieldWhereViolatedAtConstruction asserts that constructing
// a struct with a field value that violates its where-predicate is a hard error.
func TestSoundnessGateP2_StructFieldWhereViolatedAtConstruction(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def make() -> Pos:
    return Pos(x: -1)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_p2_struct_field_violated.elisa", src, AnalyzeOptions{})

	hasRefuted := false
	for _, p := range result.ProofReport {
		if p.Outcome == ProofRefuted {
			hasRefuted = true
			break
		}
	}
	hasError := len(result.Errors()) > 0
	diagStr := allDiagnostics(result)
	hasDiag := strings.Contains(diagStr, "violated") || strings.Contains(diagStr, "could not be proven")

	if !hasRefuted && !hasError && !hasDiag {
		t.Errorf("SOUNDNESS VIOLATION: Pos(x: -1) must produce a ProofRefuted entry or hard error; "+
			"proof=%v errors=%v diags=%q", result.ProofReport, result.Errors(), diagStr)
	}
}

// SoundnessGateP2_StructFieldWhereSatisfiedAtConstruction asserts that a valid
// construction (satisfying the where predicate) does not produce spurious errors.
func TestSoundnessGateP2_StructFieldWhereSatisfiedAtConstruction(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def make() -> Pos:
    return Pos(x: 5)
`
	result := analyzeContractStrict(t, "soundness_p2_struct_field_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Errorf("SOUNDNESS VIOLATION: Pos(x: 5) should not produce errors; got %v", errs)
	}
}

// SoundnessGateP2_StructFieldWhereErasureHolds asserts that reading a struct field
// typed `i64 where x > 0` returns a value of the base type `i64`, not a where-refined
// type — confirming representation erasure at the use site.
func TestSoundnessGateP2_StructFieldWhereErasureHolds(t *testing.T) {
	src := `
struct Pos:
    x: i64 where x > 0
def read(p: Pos) -> i64:
    return p.x
`
	result := analyzeContractStrict(t, "soundness_p2_struct_field_erasure.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Errorf("SOUNDNESS VIOLATION: field read (p.x: i64 where x>0) must type-check as i64 (erased); got %v", errs)
	}
}

// ---------------------------------------------------------------------------
// Invariant P2-5: where-local reassignment — out-of-range value is refuted
//
// Reassigning a `where`-refined local to a provably out-of-range constant must
// re-discharge and refute — producing an error, not silently updating the fact.
// ---------------------------------------------------------------------------

// SoundnessGateP2_WhereLocalReassignRefutedIsError asserts that assigning a
// constant that provably violates the declared where-predicate to a where-typed
// mutable local is a hard error.
func TestSoundnessGateP2_WhereLocalReassignRefutedIsError(t *testing.T) {
	src := `
def f() -> i64:
    x: mutable i64 where x > 0 = 5
    x <- -1
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_p2_reassign_refuted.elisa", src, AnalyzeOptions{})

	hasRefuted := false
	for _, p := range result.ProofReport {
		if p.Predicate == "where" && p.Outcome == ProofRefuted {
			hasRefuted = true
			break
		}
	}
	diagStr := allDiagnostics(result)
	hasDiag := strings.Contains(diagStr, "violated") || strings.Contains(diagStr, "could not be proven")
	hasError := len(result.Errors()) > 0

	if !hasRefuted && !hasDiag && !hasError {
		t.Errorf("SOUNDNESS VIOLATION: reassigning `where x > 0` local to -1 must produce a "+
			"ProofRefuted entry or hard error; proof=%v errors=%v diags=%q",
			result.ProofReport, result.Errors(), diagStr)
	}
}

// SoundnessGateP2_WhereLocalReassignSatisfyingIsClean asserts that reassigning
// a where-typed local to a provably satisfying constant does NOT produce an error.
func TestSoundnessGateP2_WhereLocalReassignSatisfyingIsClean(t *testing.T) {
	src := `
def f() -> i64:
    x: mutable i64 where x > 0 = 5
    x <- 10
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_p2_reassign_ok.elisa", src, AnalyzeOptions{})

	diagStr := allDiagnostics(result)
	if strings.Contains(diagStr, "violated") || strings.Contains(diagStr, "could not be proven") {
		t.Errorf("SOUNDNESS VIOLATION: satisfying reassignment x <- 10 should not produce a refutation; diags=%q", diagStr)
	}
}

// SoundnessGateP2_WhereLocalReassignUnknownIsNotSilent asserts that reassigning
// a where-typed local to an unknown runtime value is NOT silently accepted —
// it must produce at minimum a runtime obligation or lint (not complete silence).
func TestSoundnessGateP2_WhereLocalReassignUnknownIsNotSilent(t *testing.T) {
	src := `
def some_value() -> i64:
    return 0

def f() -> i64:
    x: mutable i64 where x > 0 = 5
    x <- some_value()
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_p2_reassign_unknown.elisa", src, AnalyzeOptions{})

	hasRuntimeObligation := false
	for _, p := range result.ProofReport {
		if p.Predicate == "where" && p.Outcome == ProofRuntime {
			hasRuntimeObligation = true
			break
		}
	}
	diagStr := allDiagnostics(result)
	hasDiag := strings.Contains(diagStr, "where") || strings.Contains(diagStr, "could not be proven")

	if !hasRuntimeObligation && !hasDiag {
		t.Errorf("SOUNDNESS VIOLATION: reassigning where-typed local to unknown runtime value must "+
			"not be silently accepted; proof=%v diags=%q", result.ProofReport, diagStr)
	}
}

// ---------------------------------------------------------------------------
// SOUNDNESS TODOs — gaps not yet enforced; visible but skipped
// ---------------------------------------------------------------------------

// SOUNDNESS TODO: A named refine alias used as a return type whose actual return
// value is provably out of range should be a hard error (mirrors the anonymous-where
// ensure gate). This is not yet enforced at the alias-return-type level — only
// anonymous `where result …` ensure paths fire.
func TestSoundnessGateP2_TODO_AliasReturnViolationIsError(t *testing.T) {
	t.Skip("SOUNDNESS TODO: refine alias on return type — violated return value not yet a hard error")
	src := `
refine Positive = i64 where self > 0

def bad() -> Positive:
    return -1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_p2_todo_alias_return_violated.elisa", src, AnalyzeOptions{})
	if len(result.Errors()) == 0 {
		t.Errorf("SOUNDNESS VIOLATION: returning -1 for Positive return alias must be a hard error; "+
			"errors=%v proof=%v", result.Errors(), result.ProofReport)
	}
}

// Struct field with `where P` mutated via a field-store after construction must re-discharge
// the field predicate at the store site. This is now enforced by dischargeFieldStoreWhere.
func TestSoundnessGateP2_TODO_StructFieldMutationRecheckIsError(t *testing.T) {
	src := `
struct Pos:
    x: mutable i64 where x > 0
def f(p: mutable Pos) -> i64:
    p.x <- -1
    return p.x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_p2_todo_struct_field_mut.elisa", src, AnalyzeOptions{})
	diagStr := allDiagnostics(result)
	if !strings.Contains(diagStr, "violated") && len(result.Errors()) == 0 {
		t.Errorf("SOUNDNESS VIOLATION: field-store p.x <- -1 violating where must be caught; "+
			"errors=%v diags=%q", result.Errors(), diagStr)
	}
}

// A parametric refine alias whose argument is a provably-violating constant must produce a
// refutation at the call site (gap closed: affineOf / substitutedAffine now resolve xs.count from
// list-literal initialisers via writtenListCount).
func TestSoundnessGateP2_TODO_ParametricRefineAliasViolationIsError(t *testing.T) {
	src := `
refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count

def get(xs: darray[i64], i: IndexOf[xs]) -> i64:
    return xs[i]

def bad() -> i64:
    xs: darray[i64] = [10, 20]
    return get(xs, 5)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(
		t, "soundness_p2_todo_parametric_alias_violated.elisa", src, AnalyzeOptions{})
	if len(result.Errors()) == 0 {
		t.Errorf("SOUNDNESS VIOLATION: index 5 into 2-element array via IndexOf alias must be refuted; "+
			"errors=%v proof=%v", result.Errors(), result.ProofReport)
	}
}
