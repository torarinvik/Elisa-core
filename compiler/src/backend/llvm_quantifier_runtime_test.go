//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// A range-bounded `forall` in an `ensure` and a loop `invariant` must lower to a runtime loop at -O0
// (debug contract checks); before, QuantifierExpr was an "unsupported expression" and blocked codegen.
func TestRangeForallContractLowersAtO0(t *testing.T) {
	result := parseAndAnalyzeBackendTestWithSMT(t, "quant_runtime.elisa", `def all_small(xs: darray[i64]&) -> bool:
	ensure result == (forall k in 0..<xs.count: xs[k] < 10)
	for index in 0..<xs.count |xs|:
		invariant index <= xs.count
		invariant forall k in 0..<index: xs[k] < 10
		false return if xs[index] >= 10
	return true

def any_big(xs: darray[i64]&) -> bool:
	ensure result == (exists k in 0..<xs.count: xs[k] >= 10)
	return not all_small(xs)
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, want := range []string{"quant.head", "quant.body", "quant.found", "quant.done"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in -O0 IR for a range quantifier contract:\n%s", want, output)
		}
	}
}
