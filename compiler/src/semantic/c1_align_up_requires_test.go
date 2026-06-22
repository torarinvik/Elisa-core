package semantic

import "testing"

// C1: an overflow-guarded postcondition discharges from an entry `requires`.
// With the requires `value + alignment <= 2^64-1`, both ensures must prove.
func TestC1AlignUpWithRequiresProves(t *testing.T) {
	src := `
def align_up(value: u64, alignment: u64) -> u64:
    requires value + alignment <= 0xFFFFFFFFFFFFFFFF
    ensure result >= value
    ensure alignment == 0 or (result % alignment) == 0
    if alignment == 0: return value
    return ((value + alignment - 1) / alignment) * alignment
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "c1_align_up.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("align_up WITH requires should prove both ensures, got:\n%s", allDiagnostics(r))
	}
}

// C1 soundness: WITHOUT the requires, `ensure result >= value` has a genuine
// counterexample (value=2^64-1, value+alignment-1 wraps) and must be REFUTED.
func TestC1AlignUpWithoutRequiresRefuted(t *testing.T) {
	src := `
def align_up(value: u64, alignment: u64) -> u64:
    ensure result >= value
    ensure alignment == 0 or (result % alignment) == 0
    if alignment == 0: return value
    return ((value + alignment - 1) / alignment) * alignment
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "c1_align_up_nogate.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("align_up WITHOUT requires must NOT prove `ensure result >= value` (overflow wrap), got:\n%s", allDiagnostics(r))
	}
}
