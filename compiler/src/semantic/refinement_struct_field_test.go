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

// Written-constant fact for a directly assigned struct field: `s.f <- 5; use s.f` should prove a
// refinement over `s.f` from the written constant without requiring the field to be set in a struct
// literal. The field-write path (writtenField) must be consulted by affineOf in addition to the
// struct-literal path (writtenStruct/writtenConst).

func TestWrittenConstFieldDirectAssignProves(t *testing.T) {
	// `s.val <- 5` on a mutable field, then `check(s.val)` with `requires x >= 5`.
	// The written-const fact for the field place should discharge the precondition.
	src := `
struct Counter:
    val: mutable i64
def check(x: i64):
    requires x >= 5
def caller():
    s: Counter = Counter{val: 0}
    s.val <- 5
    check(s.val)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "wc_field_direct.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("written-const field fact should discharge requires, got:\n%s", allDiagnostics(r))
	}
}

func TestWrittenConstFieldDirectAssignSoundnessNonConst(t *testing.T) {
	// After `s.f <- x` (non-const), the written-const fact must be dropped so a refinement over `s.f`
	// correctly declines rather than carrying the stale constant from the prior `s.f <- 5`.
	src := `
struct Counter:
    val: mutable i64
def check(x: i64):
    requires x >= 5
def caller(x: i64):
    s: Counter = Counter{val: 0}
    s.val <- 5
    s.val <- x
    check(s.val)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "wc_field_nonconst.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("non-const field write must drop the written-const fact, got:\n%s", allDiagnostics(r))
	}
}

func TestWrittenConstFieldDirectAssignSoundnessRootReassign(t *testing.T) {
	// After whole-struct reassignment (via mutable ref param), all field facts must be dropped.
	// We use a mutating callee that takes the whole struct as mutable ref — same soundness vector.
	src := `
struct Counter:
    val: mutable i64
def reset(self: mutable Counter&) -> void:
    self.val <- 0
def check(x: i64):
    requires x >= 5
def caller():
    s: Counter = Counter{val: 0}
    s.val <- 5
    reset(&s)
    check(s.val)
`
	r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "wc_field_rootreassign.elisa", src, AnalyzeOptions{EnforceStrictProofs: true, EnableSMT: true})
	if !contains(allDiagnostics(r), "could not be proven") {
		t.Fatalf("mutating callee must drop field written-const facts, got:\n%s", allDiagnostics(r))
	}
}
