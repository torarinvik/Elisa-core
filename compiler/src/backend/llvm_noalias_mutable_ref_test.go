//go:build cgo

package backend

import (
	"strings"
	"testing"
)

// defineLineHasNoalias reports whether any function `define` header in the module
// carries a `noalias` parameter attribute. Scoping to the define line avoids false
// positives from the module name / source_filename (which may contain the substring).
func defineLineHasNoalias(ir string) bool {
	for _, line := range strings.Split(ir, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "define ") && strings.Contains(trimmed, "noalias") {
			return true
		}
	}
	return false
}

// A `mutable i32&` parameter is the unique mutator of its pointee (the aliasing
// checker rejects a second live mutable reference to the same storage), so under
// ELISACORE_NOALIAS_MUTABLE_REFS it carries LLVM `noalias` — the restrict promise
// the optimizer needs to stop reloading across stores through the pointer.
const noaliasScalarRefSrc = `def bump(x: mutable i32&) -> void:
    x <- x + 1
`

func TestNoaliasStampedOnScalarMutableRef(t *testing.T) {
	t.Setenv("ELISACORE_NOALIAS_MUTABLE_REFS", "1")
	result := parseAndAnalyzeBackendTest(t, "na_scalar.elisa", noaliasScalarRefSrc)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !defineLineHasNoalias(output) {
		t.Fatalf("expected noalias on a scalar mutable-ref param under opt-in, got:\n%s", output)
	}
}

// Without the opt-in the attribute must be absent: a noalias miscompile is silent,
// so the stamp stays gated behind the explicit env flag.
func TestNoaliasAbsentByDefault(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "na_default.elisa", noaliasScalarRefSrc)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if defineLineHasNoalias(output) {
		t.Fatalf("expected NO noalias without the opt-in flag, got:\n%s", output)
	}
}

// Struct-ref pointees are EXCLUDED from the subset (open laundering residuals:
// field-rebind, nested carriers), so no noalias even with the flag on.
const noaliasStructRefSrc = `struct P:
    x: mutable i32

def bump(p: mutable P&) -> void:
    p.x <- p.x + 1
`

func TestNoaliasNotStampedOnStructRef(t *testing.T) {
	t.Setenv("ELISACORE_NOALIAS_MUTABLE_REFS", "1")
	result := parseAndAnalyzeBackendTest(t, "na_struct.elisa", noaliasStructRefSrc)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if defineLineHasNoalias(output) {
		t.Fatalf("expected NO noalias on a struct mutable-ref param (excluded subset), got:\n%s", output)
	}
}

// An IMMUTABLE scalar ref is also excluded: another mutable ref could legitimately
// be writing the pointee during the call, so a read-side restrict promise is unsound.
const noaliasImmutableRefSrc = `def peek(x: i32&) -> i32:
    return x + 1
`

func TestNoaliasNotStampedOnImmutableRef(t *testing.T) {
	t.Setenv("ELISACORE_NOALIAS_MUTABLE_REFS", "1")
	result := parseAndAnalyzeBackendTest(t, "na_immutable.elisa", noaliasImmutableRefSrc)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if defineLineHasNoalias(output) {
		t.Fatalf("expected NO noalias on an immutable ref param, got:\n%s", output)
	}
}
