package semantic

import (
	"strings"
	"testing"
)

// Each byte has a successor modulo 256, so this function has no fixed point on
// the quantified range. A call beneath a binder must not become one constant.
func TestSMTQuantifiedCallDoesNotInventFixedPoint(t *testing.T) {
	result := analyzeContractStrict(t, "quantified_call_fixed_point.elisa", `def rotate(index: usize) -> u8:
    return ((index + 1) % 256).u8()

def wrong() -> i64:
    ensure exists index in 0..<256: rotate(index) == index
    return 0
`)
	if !strings.Contains(allDiagnostics(result), "could not be proven") {
		t.Fatalf("false quantified fixed point accepted:\n%s", allDiagnostics(result))
	}
}

func TestSMTQuantifiedBoolCallDoesNotBecomeConstant(t *testing.T) {
	result := analyzeContractStrict(t, "quantified_bool_call.elisa", `def is_one(index: usize) -> bool:
    return index == 1

def wrong() -> i64:
    ensure exists index in 0..<2: is_one(index) == (index == 0)
    return 0
`)
	if !strings.Contains(allDiagnostics(result), "could not be proven") {
		t.Fatalf("false quantified Boolean call accepted:\n%s", allDiagnostics(result))
	}
}

func TestSMTQuantifiedIndependentCallKeepsFacts(t *testing.T) {
	result := analyzeContractStrict(t, "quantified_independent_call.elisa", `def seven() -> u8:
    return 7

def valid() -> i64:
    ensure forall index in 0..<8: seven() == 7
    return 0
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("binder-independent call declined:\n%s", allDiagnostics(result))
	}
}

func TestSMTQuantifiedAggregateCallDoesNotInventFixedPoint(t *testing.T) {
	result := analyzeContractStrict(t, "quantified_aggregate_call.elisa", `struct ByteBox:
    value: u8

def rotate(index: usize) -> ByteBox:
    return ByteBox{value: ((index + 1) % 256).u8()}

def wrong() -> i64:
    ensure exists index in 0..<256: rotate(index).value == index
    return 0
`)
	if !strings.Contains(allDiagnostics(result), "could not be proven") {
		t.Fatalf("false quantified aggregate fixed point accepted:\n%s", allDiagnostics(result))
	}
}

func TestSMTQuantifiedAggregateBoolCallDoesNotBecomeConstant(t *testing.T) {
	result := analyzeContractStrict(t, "quantified_aggregate_bool_call.elisa", `struct FlagBox:
    value: bool

def flag(index: usize) -> FlagBox:
    return FlagBox{value: index == 1}

def wrong() -> i64:
    ensure exists index in 0..<2: flag(index).value == (index == 0)
    return 0
`)
	if !strings.Contains(allDiagnostics(result), "could not be proven") {
		t.Fatalf("false quantified aggregate Boolean call accepted:\n%s", allDiagnostics(result))
	}
}
