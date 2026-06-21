//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// docs/97: a function that `uses` a named contract inherits its requires/ensure, and when the body
// honours them the proof discharges under -strict. One contract, two functions sharing it.
func TestNamedContractProvesUnderStrict(t *testing.T) {
	src := `
contract NonNegOut(out: i64, src: i64):
    requires src >= 0
    ensure result >= src

def copy_floor(s: i64) -> i64:
    uses NonNegOut(0, s)
    return s

def echo_floor(s: i64) -> i64:
    uses NonNegOut(0, s)
    return s
`
	result := analyzeContractStrict(t, "named_contract_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("both functions honour NonNegOut and should prove under -strict, got: %v", errs)
	}
}

// A function that `uses` a contract but VIOLATES its ensure must be a hard error under -strict.
func TestNamedContractViolationErrors(t *testing.T) {
	src := `
contract NonNegOut(out: i64, src: i64):
    requires src >= 0
    ensure result >= src

def bad(s: i64) -> i64:
    uses NonNegOut(0, s)
    return s - 1
`
	result := analyzeContractStrict(t, "named_contract_bad.elisa", src)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("`return s - 1` violates the inherited ensure `result >= src`; must error, got: %v", result.Errors())
	}
}

// The inherited precondition is checked at CALL SITES of the applying function: calling it with an
// argument that fails the contract's `requires` is a hard error.
func TestNamedContractRequiresCheckedAtCallSite(t *testing.T) {
	src := `
contract NonNegSrc(src: i64):
    requires src >= 0
    ensure result >= 0

def use_it(s: i64) -> i64:
    uses NonNegSrc(s)
    return s

def caller() -> i64:
    return use_it(0 - 5)
`
	result := analyzeContractStrict(t, "named_contract_callsite.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatalf("calling use_it(-5) violates the inherited `requires src >= 0`; must error, got none")
	}
}

func TestNamedContractIncludedValueLawRequiresAtCallSite(t *testing.T) {
	src := `
law NonNeg(x: i64) = x >= 0

contract NonNegSrc(src: i64):
    includes NonNeg(src)
    ensure result >= src

def use_it(s: i64) -> i64:
    uses NonNegSrc(s)
    return s

def caller() -> i64:
    return use_it(0 - 5)
`
	result := analyzeContractStrict(t, "named_contract_included_law_callsite.elisa", src)
	if len(result.Errors()) == 0 {
		t.Fatalf("included value law must become an inherited requires checked at call sites, got none")
	}
}

func TestNamedContractGenericSpecializationProves(t *testing.T) {
	src := `
contract Monotonic[T](lo: T, hi: T):
    requires lo <= hi
    ensure result >= lo

def clamp_floor(x: i64) -> i64:
    uses Monotonic[i64](0, 100)
    requires x >= 0
    return 100
`
	result := analyzeContractStrict(t, "named_contract_generic_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("generic contract specialized at i64 should prove, got: %v", errs)
	}
}

func TestNamedContractGenericTypeArgumentMismatchErrors(t *testing.T) {
	src := `
contract Monotonic[T](lo: T, hi: T):
    requires lo <= hi
    ensure result >= lo

def bad(x: i64) -> i64:
    uses Monotonic[bool](0, 100)
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_contract_generic_bad_typearg.elisa", src, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "contract `Monotonic` argument 1 (lo) expects bool") {
		t.Fatalf("mismatched explicit contract type argument must error, got:\n%s", allDiagnostics(result))
	}
}

func TestNamedContractIncludedEffectLawStaysEffectObligation(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate

contract PureUse(x: i64):
    includes NoAlloc()

def grow(x: i64) -> i64:
    uses PureUse(x)
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(x)
        return xs[0]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_contract_included_effect.elisa", src, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "uses the `Memory.Allocate` effect") {
		t.Fatalf("included effect law must fold as an effect obligation, got:\n%s", allDiagnostics(result))
	}
}

// Frame conditions in a contract propagate: a function that `uses` a contract whose `changes` set is
// {out} may write that place, but writing a place OUTSIDE it is a frame violation.
func TestNamedContractFramePropagates(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    py: mutable i32
    health: mutable i32

contract MovesOnly(r: mutable Render&):
    changes r.px, r.py

def good(r: mutable Render&) -> void:
    uses MovesOnly(r)
    r.px <- 1
    r.py <- 2

def bad(r: mutable Render&) -> void:
    uses MovesOnly(r)
    r.health <- 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_contract_frame.elisa", src, AnalyzeOptions{})
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "outside the `changes` set") {
		t.Fatalf("writing r.health outside the contract's changes set must error, got:\n%s", diags)
	}
}

// Applying an unknown contract is a hard error.
func TestNamedContractUnknownErrors(t *testing.T) {
	src := `
def f(s: i64) -> i64:
    uses NoSuchContract(s)
    return s
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_contract_unknown.elisa", src, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "unknown contract") {
		t.Fatalf("applying an undeclared contract must error, got:\n%s", allDiagnostics(result))
	}
}

// Arity mismatch between `uses` and the contract's parameters is a hard error.
func TestNamedContractArityErrors(t *testing.T) {
	src := `
contract Two(a: i64, b: i64):
    requires a >= b

def f(x: i64) -> i64:
    uses Two(x)
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_contract_arity.elisa", src, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "argument(s)") {
		t.Fatalf("arity mismatch must error, got:\n%s", allDiagnostics(result))
	}
}

func TestNamedContractAcceptsMatchedFormalTypes(t *testing.T) {
	src := `
contract Flagged(ok: bool, value: i64):
    requires ok
    ensure result >= value

def f(ok: bool, x: i64) -> i64:
    uses Flagged(ok, x)
    return x
`
	result := analyzeContractStrict(t, "named_contract_formals_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("uses arguments matching contract formal types should analyze cleanly, got: %v", errs)
	}
}

func TestNamedContractRejectsMismatchedFormalTypes(t *testing.T) {
	src := `
contract NeedsBool(ok: bool):
    requires ok

def f(x: i64) -> i64:
    uses NeedsBool(x)
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "named_contract_formals_bad.elisa", src, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "contract `NeedsBool` argument 1 (ok) expects bool, got i64") {
		t.Fatalf("uses argument whose type mismatches the contract formal must error, got:\n%s", allDiagnostics(result))
	}
}

// Integration: value-law refinement, scoped proof automation, named-contract expansion, and
// typestate ensures all meet on one function. The value proof may discharge value obligations, but it
// must not launder a false named-state postcondition across the docs/85 discharge-class boundary.
func TestNamedContractValueLawScopedProofDoesNotLaunderTypestate(t *testing.T) {
	src := `
law NonNeg(x: i64) = x >= 0

lemma nonneg_implies_ge_zero(x: i64):
    requires x is NonNeg
    ensure x >= 0
    pass

contract EnergyResult(energy: i64):
    requires energy is NonNeg
    ensure result >= energy

struct Player[state Alive | Dead]:
    health: mutable i64
    score: mutable i64

    derive state:
        Alive when self.health > 0
        Dead when self.health <= 0

def reward_ok(p: mutable Player[Alive]&, energy: i64 is NonNeg) -> i64 ensures p => Alive:
    uses EnergyResult(energy)
    assert energy >= 0 by scoped:
        nonneg_implies_ge_zero(energy)
    p.score <- p.score + 1
    return energy

def reward_bad(p: mutable Player[Alive]&, energy: i64 is NonNeg) -> i64 ensures p => Alive:
    uses EnergyResult(energy)
    assert energy >= 0 by scoped:
        nonneg_implies_ge_zero(energy)
    p.health <- 0
    return energy
`
	result := analyzeContractStrict(t, "named_contract_typestate_discharge_class.elisa", src)
	diags := allDiagnostics(result)
	if !strings.Contains(diags, "cannot prove ensures p => Alive") {
		t.Fatalf("value-law + named-contract proofs must not discharge a false typestate ensure, got:\n%s", diags)
	}
	if strings.Contains(diags, "reward_ok") {
		t.Fatalf("the preserving function should satisfy all combined obligations; got:\n%s", diags)
	}
}
