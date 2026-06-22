package semantic

import "testing"

// Fix for the shadPS4 "savedata-01" verification finding: a `requires`/refinement over a struct-field
// place must be dischargeable from the field's construction-time constant. `read(v, 0)` where
// `v = AbiView{size: 64}` should prove `off + 4 <= v.size` (4 <= 64); a violating call must still be
// rejected; and a field whose value is unknown must decline (no false positive).
func TestRequiresOverStructLiteralFieldDischarges(t *testing.T) {
	src := `
struct AbiView:
    base: void&
    size: usize
def read(v: AbiView, off: usize) -> u32:
    requires off + 4 <= v.size
    return 0
def good(p: void&) -> u32:
    v: AbiView = AbiView{base: p, size: 64}
    return read(v, 0)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rsf_good.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("requires over a struct-literal field constant should discharge, got:\n%s", allDiagnostics(r))
	}
}

func TestRequiresOverStructLiteralFieldRejectsViolation(t *testing.T) {
	src := `
struct AbiView:
    base: void&
    size: usize
def read(v: AbiView, off: usize) -> u32:
    requires off + 4 <= v.size
    return 0
def bad(p: void&) -> u32:
    v: AbiView = AbiView{base: p, size: 64}
    return read(v, 100)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rsf_bad.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "precondition of \"read\"") {
		t.Fatalf("a provably-violated precondition over a struct field must be rejected, got:\n%s", allDiagnostics(r))
	}
}

func TestRequiresOverUnknownStructFieldDeclines(t *testing.T) {
	// `size` comes from a parameter, not a literal: the prover cannot know it, so the call must NOT be
	// silently proven (it falls through to the strict "could not be proven" diagnostic).
	src := `
struct AbiView:
    base: void&
    size: usize
def read(v: AbiView, off: usize) -> u32:
    requires off + 4 <= v.size
    return 0
def caller(p: void&, n: usize) -> u32:
    v: AbiView = AbiView{base: p, size: n}
    return read(v, 0)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "rsf_unknown.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("an unknown struct-field bound must decline, not falsely prove, got:\n%s", allDiagnostics(r))
	}
}
