//go:build cgo

package semantic

import (
	"testing"
)

// A postcondition that holds by reflexivity once `result` is replaced by the returned expression
// is proven even when its operands are outside the SMT fragment (a string guard, a pure call).
func TestEnsureProvenByReflexivity(t *testing.T) {
	src := `
def cpuid_mask() -> u64:
    return 3

def rdtsc_mask() -> u64:
    return 5

def trim(text: sview) -> sview:
    return text

def implicit_mask(op: sview) -> u64:
    ensure op == "cpuid" implies result == cpuid_mask()
    return cpuid_mask()

def easm_trim(text: sview) -> sview:
    ensure result == trim(text)
    return trim(text)
`
	result := analyzeContractStrict(t, "reflexive.elisa", src)
	if n := ensureFailuresFor(result.Errors(), "implicit_mask") + ensureFailuresFor(result.Errors(), "easm_trim"); n != 0 {
		t.Fatalf("every clause holds by reflexivity; got: %v", result.Errors())
	}
}

// Reflexivity must not cross a shadowing binding: the returned `g(x)` reads the local x, the
// clause's `g(x)` the parameter.
func TestEnsureReflexivityDeclinesUnderShadowing(t *testing.T) {
	src := `
def g(v: i64) -> i64:
    return v

def shadowed(x: i64) -> i64:
    ensure result == g(x)
    if x > 0:
        x: i64 = 7
        return g(x)
    return g(x)
`
	result := analyzeContractStrict(t, "reflexive_shadow.elisa", src)
	if ensureFailuresFor(result.Errors(), "shadowed") == 0 {
		t.Fatalf("g(7) != g(x) for x != 7: the shadowed return must stay unproven; got: %v", result.Errors())
	}
}

// NaN != NaN: a float operand is never equal to itself by reflexivity.
func TestEnsureReflexivityDeclinesFloats(t *testing.T) {
	src := `
def nan_like(v: f64) -> f64:
    return v / v

def float_self(v: f64) -> f64:
    ensure result == nan_like(v)
    return nan_like(v)
`
	result := analyzeContractStrict(t, "reflexive_float.elisa", src)
	if ensureFailuresFor(result.Errors(), "float_self") == 0 {
		t.Fatalf("a float result can be NaN, which is not == itself; got: %v", result.Errors())
	}
}
