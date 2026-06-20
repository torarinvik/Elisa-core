//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// ERASURE: a `ghost` variable is verification-only and must be fully absent from generated code —
// no alloca, no store, and the distinctive constant in its initializer must not appear in the IR.
// Real code around it (the returned real value) is unaffected.
func TestGhostVarErasedFromLLVMIR(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_ghost_erasure.elisa", `def f(x: i64) -> i64:
    ghost g: i64 = x + 987654321
    invariant g > x
    return x
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if strings.Contains(output, "987654321") {
		t.Fatalf("ghost initializer constant leaked into generated IR:\n%s", output)
	}
}

// ERASURE: a grouped `ghost:` block is likewise fully erased — none of its initializer constants
// reach the IR.
func TestGhostBlockErasedFromLLVMIR(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "backend_ghost_block_erasure.elisa", `def h(x: i64) -> i64:
    ghost:
        a: i64 = x + 111222333
        b: i64 = x + 444555666
    invariant b > a
    return x
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	for _, c := range []string{"111222333", "444555666"} {
		if strings.Contains(output, c) {
			t.Fatalf("ghost block initializer constant %s leaked into generated IR:\n%s", c, output)
		}
	}
}
