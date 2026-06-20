package semantic

import (
	"strings"
	"testing"
)

// A refinement-typed index whose proven interval lies within a constant-size array's extent indexes it
// with NO runtime bounds check. This is the decoder payoff: `regs[d.sdst]` where sdst : InRange[0,127]
// into an array[u32, 128] is statically in bounds. Three refinement sources are honored: a refined
// field read, a refined-return call, and an immutable refined parameter.
func TestRefinedIndexProvesArrayBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "refined_index.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Sop2:
    sdst: mutable u32 is InRange[0, 127]
def read_field(d: Sop2&, regs: array[u32, 128]&) -> u32:
    return regs[d.sdst]
def sop2_sdst(inst: u32) -> u32 is InRange[0, 127]:
    return (inst >> 16) & 0x7f
def read_call(inst: u32, regs: array[u32, 128]&) -> u32:
    return regs[sop2_sdst(inst)]
def read_param(idx: u32 is InRange[0, 127], regs: array[u32, 128]&) -> u32:
    return regs[idx]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, "unchecked index requires") {
		t.Fatalf("a refined [0,127] index into array[u32,128] must prove in bounds, got:\n%s", all)
	}
}

// SOUNDNESS: a refinement interval that can EXCEED the array extent must NOT prove in bounds. A
// [0,255] index into a [128] array is out of range for idx in 128..255 — the bridge must decline.
func TestRefinedIndexTooWideDeclines(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "wide_index.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Inst:
    sel: mutable u32 is InRange[0, 255]
def read_field(d: Inst&, regs: array[u32, 128]&) -> u32:
    return regs[d.sel]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("a [0,255] refinement does not fit [128]; the check must NOT be eliminated, got:\n%s", allDiagnostics(result))
	}
}

// SOUNDNESS: a MUTABLE refined parameter carries no body-wide interval (it could be reassigned out of
// range), so it must not silently eliminate the bounds check.
func TestMutableRefinedParamIndexDeclines(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "mut_param_index.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
def read_reg(idx: mutable u32 is InRange[0, 127], regs: array[u32, 128]&) -> u32:
    idx <- 200
    return regs[idx]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("a mutable refined param can drift out of range; bounds check must remain, got:\n%s", allDiagnostics(result))
	}
}

// SOUNDNESS: a non-range law (here parity, not an interval) yields no numeric interval, so it cannot
// discharge an index bound even though the value is refined.
func TestNonRangeLawIndexDeclines(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "nonrange_index.elisa", `law Even(self: u32) = (self % 2) == 0
struct W:
    k: mutable u32 is Even
def read_reg(d: W&, regs: array[u32, 128]&) -> u32:
    return regs[d.k]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("an Even refinement is not an interval; it cannot prove an index bound, got:\n%s", allDiagnostics(result))
	}
}
