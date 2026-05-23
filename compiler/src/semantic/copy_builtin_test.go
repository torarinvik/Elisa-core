package semantic

import (
	"strings"
	"testing"
)

// copy[array[T, N]](src) materializes a fixed-size, stack-owned array from a
// fixed-size array source. No region is required (it lives on the stack).
func TestAnalyzeCopyBuiltinSupportsFixedArray(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "copy_builtin_fixed_array.elisa", `def dup(src: array[u8, 4]) -> array[u8, 4]:
    return copy[array[u8, 4]](src)
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for fixed-array copy, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Copying a runtime-length source (darray) to a stack array cannot prove a
// compile-time size, so it is rejected with a pointer at clone.
func TestAnalyzeCopyBuiltinRejectsRuntimeLengthSource(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "copy_builtin_runtime_len.elisa", `def dup(src: darray[u8]) -> array[u8, 4]:
    return copy[array[u8, 4]](src)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "use clone") {
		t.Fatalf("expected runtime-length copy rejection pointing at clone, got:\n%s", all)
	}
}

// copy's target must be a fixed-size array; a scalar target is rejected.
func TestAnalyzeCopyBuiltinRejectsNonArrayTarget(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "copy_builtin_scalar_target.elisa", `def dup(src: array[u8, 4]) -> u32:
    return copy[u32](src)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "copy target must be a fixed-size array") {
		t.Fatalf("expected non-array target rejection, got:\n%s", all)
	}
}

// Mismatched sizes between source and target fixed arrays are rejected.
func TestAnalyzeCopyBuiltinRejectsSizeMismatch(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "copy_builtin_size_mismatch.elisa", `def dup(src: array[u8, 8]) -> array[u8, 4]:
    return copy[array[u8, 4]](src)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "copy cannot copy") {
		t.Fatalf("expected size-mismatch rejection, got:\n%s", all)
	}
}
