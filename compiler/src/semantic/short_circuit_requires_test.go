//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

const shortCircuitRequiresHelper = `def needs_nonnegative(n: i64) -> bool:
    requires n >= 0
    return true
`

func TestShortCircuitCallRequires(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		reject     bool
	}{
		{"and_true_path", "return n >= 0 and needs_nonnegative(n)", false},
		{"or_false_path", "return n < 0 or needs_nonnegative(n)", false},
		{"and_insufficient_guard", "return n <= 10 and needs_nonnegative(n)", true},
		{"or_wrong_path", "return n >= 0 or needs_nonnegative(n)", true},
		{"no_guard_leak", "selected: bool = n >= 0 and needs_nonnegative(n)\n    return needs_nonnegative(n)", true},
		{"statement_guard", "if n >= 0:\n        return needs_nonnegative(n)\n    return false", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeContractStrict(t, "short_circuit_"+tc.name+".elisa", shortCircuitRequiresHelper+"def caller(n: i64) -> bool:\n    "+tc.body+"\n")
			if tc.reject {
				if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
					t.Fatalf("unproven RHS call accepted: %s", allDiagnostics(result))
				}
			} else if len(result.Errors()) != 0 {
				t.Fatalf("guarded RHS call rejected: %s", allDiagnostics(result))
			}
		})
	}
}

func TestShortCircuitCallRequiresTwoArguments(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_two_arguments.elisa", `def decode(first: i64, second: i64) -> bool:
    requires first >= 192 and first <= 223
    requires second >= 128 and second <= 191
    return true
def caller(first: i64, second: i64) -> bool:
    return first >= 192 and first <= 223 and second >= 128 and second <= 191 and decode(first, second)
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("guarded decoder rejected: %s", allDiagnostics(result))
	}
}

func TestShortCircuitPureHelperGuard(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_helper.elisa", shortCircuitRequiresHelper+`def nonnegative(n: i64) -> bool:
    return n >= 0
def caller(n: i64) -> bool:
    return nonnegative(n) and needs_nonnegative(n)
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("pure helper guard rejected: %s", allDiagnostics(result))
	}
}

func TestShortCircuitLongGuardChain(t *testing.T) {
	guards := strings.Repeat("n >= 0 and ", 128)
	result := analyzeContractStrict(t, "short_circuit_long_chain.elisa", shortCircuitRequiresHelper+"def caller(n: i64) -> bool:\n    return "+guards+"needs_nonnegative(n)\n")
	if len(result.Errors()) != 0 {
		t.Fatalf("long guard chain rejected: %s", allDiagnostics(result))
	}
}

func TestShortCircuitImpurePrefixDoesNotRetainOldValue(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_mutating_prefix.elisa", shortCircuitRequiresHelper+`def mutate(value: mutable i64&) -> bool:
    value <- -1
    return true
def caller(n: mutable i64) -> bool:
    return n >= 0 and mutate(&n) and needs_nonnegative(n)
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
		t.Fatalf("mutation retained a stale guard: %s", allDiagnostics(result))
	}
}

func TestShortCircuitMutableGlobalIsNotValueSnapshot(t *testing.T) {
	result := analyzeContractStrict(t, "short_circuit_shared_global.elisa", shortCircuitRequiresHelper+`global mutable counter: i64 = 0
def caller() -> bool:
    can Global.Read:
        return counter >= 0 and needs_nonnegative(counter)
`)
	if len(result.Errors()) == 0 || !strings.Contains(allDiagnostics(result), "precondition of") {
		t.Fatalf("shared storage read treated as a value snapshot: %s", allDiagnostics(result))
	}
}
