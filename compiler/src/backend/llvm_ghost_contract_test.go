//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGhostFunctionValueContractsErasedAtO0(t *testing.T) {
	for _, contract := range []string{
		"requires spec_nonnegative(n)",
		"ensure spec_nonnegative(result)",
		"ensure result == old(spec_identity(n))",
	} {
		t.Run(contract, func(t *testing.T) {
			result := parseAndAnalyzeBackendTest(t, "ghost_value_contract.elisa", `ghost def spec_nonnegative(n: i64) -> bool:
    return n >= 0
ghost def spec_identity(n: i64) -> i64:
    return n
def check(n: i64) -> i64:
    `+contract+`
    requires n >= 0
    ensure result >= 0
    return n
`)
			output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
			if err != nil {
				t.Fatalf("GenerateLLVMIRWithOpt: %v", err)
			}
			for _, symbol := range []string{"@spec_nonnegative", "@spec_identity"} {
				if strings.Contains(output, symbol) {
					t.Fatalf("erased ghost function referenced in runtime IR: %s\n%s", symbol, output)
				}
			}
			for _, message := range []string{"precondition failed", "postcondition failed"} {
				if !strings.Contains(output, message) {
					t.Fatalf("ordinary clause after ghost clause was erased: %s\n%s", message, output)
				}
			}
		})
	}
}
