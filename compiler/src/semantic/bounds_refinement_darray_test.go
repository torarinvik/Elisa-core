package semantic

import (
	"strings"
	"testing"
)

// A refinement-typed index into an IMMUTABLE dynamic array whose length is pinned by a live
// `requires <darray>.count >= K` precondition (K above the index's proven max) is in bounds with no
// runtime check — the darray analogue of the constant-size array elision. Covers emulator memory
// modeled as a length-bounded darray rather than a fixed `array[T, N]`.
func TestRefinedIndexProvesImmutableDArrayBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "darray_index.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
def read_reg(regs: darray[u64]&, r: u32 is InRange[0, 31]) -> u64:
    requires regs.count >= 32
    return regs[r]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, "unchecked index requires") {
		t.Fatalf("a refined [0,31] index into a darray with `requires count >= 32` must prove in bounds, got:\n%s", all)
	}
}

// SOUNDNESS: a MUTABLE darray could pop/clear and shrink its count below the precondition inside the
// body, so a `requires count >= K` is NOT a sound length guarantee at the index — the check must stay.
func TestRefinedIndexMutableDArrayDeclines(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "darray_mut.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
def read_reg(regs: mutable darray[u64]&, r: u32 is InRange[0, 31]) -> u64:
    requires regs.count >= 32
    return regs[r]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("a mutable darray can shrink below the precondition; the check must NOT be eliminated, got:\n%s", allDiagnostics(result))
	}
}

// SOUNDNESS: the precondition must strictly exceed the index's proven maximum. `requires count >= 16`
// does not cover an index in [0, 31] (count could be exactly 16, index 31 out of bounds) — decline.
func TestRefinedIndexInsufficientCountDeclines(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "darray_small.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
def read_reg(regs: darray[u64]&, r: u32 is InRange[0, 31]) -> u64:
    requires regs.count >= 16
    return regs[r]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("`requires count >= 16` does not cover an index up to 31; the check must NOT be eliminated, got:\n%s", allDiagnostics(result))
	}
}

// SOUNDNESS: without any length precondition there is no count lower bound at all, so an immutable
// darray index still needs its runtime check.
func TestRefinedIndexNoCountRequiresDeclines(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "darray_norequire.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
def read_reg(regs: darray[u64]&, r: u32 is InRange[0, 31]) -> u64:
    return regs[r]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("no length precondition: an unproven darray index must keep its runtime check, got:\n%s", allDiagnostics(result))
	}
}
