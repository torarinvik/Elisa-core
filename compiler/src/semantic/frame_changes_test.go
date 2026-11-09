package semantic

import (
	"testing"
)

// docs/87 brick 87-1: a write within the declared `changes` set is clean.
func TestChangesWriteInSetIsClean(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    py: mutable i32
    health: mutable i32

def clip_move(r: mutable Render&, dx: i32, dy: i32) changes r.px, r.py:
    r.px <- r.px + dx
    r.py <- r.py + dy
`
	result := analyzeTreeTestSource(t, "changes_in_set.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("writes within the changes set should be clean, got: %v", errs)
	}
}

// A write OUTSIDE the declared set is a compile error (the docs/87 headline).
func TestChangesWriteOutOfSetErrors(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    py: mutable i32
    health: mutable i32

def bad(r: mutable Render&) changes r.px:
    r.health <- 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "changes_out_set.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "outside the `changes` set") {
		t.Fatalf("a write to r.health outside `changes r.px` must error, got:\n%s", allDiagnostics(result))
	}
}

// A write to a LOCAL variable is never a frame violation, even under a changes clause.
func TestChangesLocalWriteAllowed(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    py: mutable i32

def f(r: mutable Render&) changes r.px:
    tmp: mutable i32 = 0
    tmp <- 5
    r.px <- tmp
`
	result := analyzeTreeTestSource(t, "changes_local.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("local writes must not trip the frame check, got: %v", errs)
	}
}

// Channel 2: passing an out-of-set place to a MUTABLE-ref callee is a write to that place → error.
func TestChangesMutableRefArgOutOfSetErrors(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    health: mutable i32

def zero(n: mutable i32&):
    n <- 0

def bad(r: mutable Render&) changes r.px:
    zero(&r.health)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "changes_refarg_out.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "outside the `changes` set") {
		t.Fatalf("passing r.health to a mutable-ref callee must error under `changes r.px`, got:\n%s", allDiagnostics(result))
	}
}

// Channel 2, in-set: passing an in-set place by mutable ref is clean.
func TestChangesMutableRefArgInSetClean(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    health: mutable i32

def zero(n: mutable i32&):
    n <- 0

def ok(r: mutable Render&) changes r.px:
    zero(&r.px)
`
	result := analyzeTreeTestSource(t, "changes_refarg_in.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("passing an in-set place by mutable ref should be clean, got: %v", errs)
	}
}

// A `changes` target that is not a parameter is rejected.
func TestChangesNonParamTargetErrors(t *testing.T) {
	src := `
struct Render:
    px: mutable i32

def f(r: mutable Render&) changes ghost.px:
    r.px <- 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "changes_nonparam.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is not a parameter") {
		t.Fatalf("a non-parameter changes target must error, got:\n%s", allDiagnostics(result))
	}
}

// docs/87 87-2: `preserves Y` forbids writing Y; a write to a preserved place is an error.
func TestPreservesWriteErrors(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    health: mutable i32

def f(r: mutable Render&) preserves r.health:
    r.px <- 1
    r.health <- 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "preserves_write.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "`preserves`") {
		t.Fatalf("writing a preserved place must error, got:\n%s", allDiagnostics(result))
	}
}

// A write to a place disjoint from the preserved set is clean (writing r.px under preserves r.health).
func TestPreservesDisjointWriteClean(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    health: mutable i32

def f(r: mutable Render&) preserves r.health:
    r.px <- 1
`
	result := analyzeTreeTestSource(t, "preserves_disjoint.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("writing a place disjoint from the preserved set should be clean, got: %v", errs)
	}
}

// preserves also blocks channel 2: passing a preserved place to a mutable-ref callee is an error.
func TestPreservesMutableRefArgErrors(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    health: mutable i32

def zero(n: mutable i32&):
    n <- 0

def f(r: mutable Render&) preserves r.health:
    zero(&r.health)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "preserves_refarg.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "`preserves`") {
		t.Fatalf("passing a preserved place to a mutable-ref callee must error, got:\n%s", allDiagnostics(result))
	}
}

// docs/87 §7 consistency: a place may not be both changed and preserved.
func TestChangesPreservesConflictErrors(t *testing.T) {
	src := `
struct Render:
    px: mutable i32

def f(r: mutable Render&) changes r.px preserves r.px:
    r.px <- 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "frame_conflict.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "conflicts with") {
		t.Fatalf("a place in both changes and preserves must error, got:\n%s", allDiagnostics(result))
	}
}

// docs/88: a function `fulfills` a frame law — writes within the law's frame are clean.
func TestFulfillsFrameLawInFrameClean(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    py: mutable i32
    health: mutable i32

law MovesPlayerOnly(self: Render&) changes self.px, self.py

def clip_move(r: mutable Render&, dx: i32) fulfills r is MovesPlayerOnly:
    r.px <- r.px + dx
    r.py <- r.py + 1
`
	result := analyzeTreeTestSource(t, "fulfills_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("writes within the fulfilled frame should be clean, got: %v", errs)
	}
}

// A write outside the fulfilled frame is a compile error (the docs/88 §13 headline).
func TestFulfillsFrameLawOutOfFrameErrors(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    py: mutable i32
    health: mutable i32

law MovesPlayerOnly(self: Render&) changes self.px, self.py

def bad(r: mutable Render&) fulfills r is MovesPlayerOnly:
    r.health <- 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "fulfills_bad.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "outside the `changes` set") {
		t.Fatalf("a write to r.health under `fulfills r is MovesPlayerOnly` must error, got:\n%s", allDiagnostics(result))
	}
}

// A frame law may also carry `preserves`; fulfilling it forbids writing the preserved place.
func TestFulfillsFrameLawWithPreserves(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    health: mutable i32

law NoHeal(self: Render&) preserves self.health

def f(r: mutable Render&) fulfills r is NoHeal:
    r.health <- 99
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "fulfills_preserves.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "`preserves`") {
		t.Fatalf("fulfilling a preserves frame law must forbid writing the preserved place, got:\n%s", allDiagnostics(result))
	}
}

// `fulfills` a VALUE law is a wrong-class error.
func TestFulfillsValueLawErrors(t *testing.T) {
	src := `
struct Render:
    px: mutable i32

law Positive(self: i32) = self > 0

def f(r: mutable Render&) fulfills r is Positive:
    r.px <- 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "fulfills_value.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "requires a frame law") {
		t.Fatalf("fulfilling a value law must error, got:\n%s", allDiagnostics(result))
	}
}

// A frame law whose subject is not a reference parameter is rejected at the law decl.
func TestFrameLawNonRefSubjectErrors(t *testing.T) {
	src := `
struct Render:
    px: mutable i32

law Bad(self: i32) changes self.px
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "framelaw_nonref.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "not a mutable reference parameter") {
		t.Fatalf("a frame law over a non-ref subject must error, got:\n%s", allDiagnostics(result))
	}
}

// docs/87 87-3: a callee's declared `changes` frame REFINES a mutable-ref argument. Passing the
// whole struct `r` to a callee that only `changes s.px` is checked as a write to `r.px` (not the
// whole `r`), so it is clean under the caller's own `changes r.px` — where the conservative
// whole-place rule would have rejected it.
func TestInterprocChangesRefinesArgClean(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    health: mutable i32

def bump(s: mutable Render&) changes s.px:
    s.px <- s.px + 1

def outer(r: mutable Render&) changes r.px:
    bump(r)
`
	result := analyzeTreeTestSource(t, "interproc_refine_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a callee frame should refine the arg to r.px and be clean under `changes r.px`, got: %v", errs)
	}
}

// The refinement is sound, not just permissive: a callee that `changes s.health` refines the arg to
// `r.health`, which is OUTSIDE the caller's `changes r.px` → error.
func TestInterprocChangesRefinesArgErrors(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    health: mutable i32

def harm(s: mutable Render&) changes s.health:
    s.health <- 0

def outer(r: mutable Render&) changes r.px:
    harm(r)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "interproc_refine_err.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "outside the `changes` set") {
		t.Fatalf("a callee that changes s.health refines the arg to r.health, which is outside `changes r.px`, got:\n%s", allDiagnostics(result))
	}
}

// A callee with NO bounding frame leaves the arg unbounded, so the conservative whole-place rule
// still applies: passing the whole `r` is a write to all of `r`, not covered by `changes r.px`.
func TestInterprocUnboundedCalleeStaysConservative(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    health: mutable i32

def wreck(s: mutable Render&):
    s.health <- 0

def outer(r: mutable Render&) changes r.px:
    wreck(r)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "interproc_unbounded.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "outside the `changes` set") {
		t.Fatalf("an unbounded callee must keep the conservative whole-place rule, got:\n%s", allDiagnostics(result))
	}
}

// A `fulfills`-derived frame is part of the effective summary too: a callee that `fulfills s is
// MovesPlayerOnly` (changes self.px) refines the arg to r.px and is clean under `changes r.px`.
func TestInterprocFulfillsRefinesArg(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    health: mutable i32

law MovesPlayerOnly(self: Render&) changes self.px

def step(s: mutable Render&) fulfills s is MovesPlayerOnly:
    s.px <- s.px + 1

def outer(r: mutable Render&) changes r.px:
    step(r)
`
	result := analyzeTreeTestSource(t, "interproc_fulfills.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a fulfills-derived callee frame should refine the arg and be clean, got: %v", errs)
	}
}

// docs/85 observability: function-level law discharges are recorded in the --explain proof report —
// effect/shape/composite as proven (contract), measure as measured.
func TestFunctionLevelLawProofReport(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate
law HotKernel includes NoAlloc, NoBoundsChecks

def kernel(xs: darray[i64]) -> i64 fulfills HotKernel:
    total: mutable i64 = 0
    for i in 0..<xs.count:
        total <- total + xs[i]
    return total

def scale(xs: darray[i64]) -> i64 fulfills Vectorizes:
    total: mutable i64 = 0
    for i in 0..<xs.count:
        total <- total + xs[i] * 2
    return total
`
	result := analyzeTreeTestSource(t, "fnlaw_proof_report.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected a clean analysis, got: %v", errs)
	}
	var contract, measured int
	for _, f := range result.ProofReport {
		switch f.Outcome {
		case ProofProvenContract:
			contract++
		case ProofMeasured:
			measured++
		}
	}
	if contract != 1 {
		t.Fatalf("expected 1 proven-contract fact (kernel fulfills HotKernel), got %d: %+v", contract, result.ProofReport)
	}
	if measured != 1 {
		t.Fatalf("expected 1 measured fact (scale fulfills Vectorizes), got %d: %+v", measured, result.ProofReport)
	}
}

// A frame law used in value `is` position (instead of `fulfills`) is a wrong-class error.
func TestFrameLawInValueIsErrors(t *testing.T) {
	src := `
struct Render:
    px: mutable i32

law MovesPlayerOnly(self: Render&) changes self.px

def f(r: mutable Render&) -> bool:
    return r is MovesPlayerOnly
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "framelaw_value_is.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is a frame law") {
		t.Fatalf("using a frame law with value `is` must error, got:\n%s", allDiagnostics(result))
	}
}

// AUDIT (frame channel): a mutating BUILTIN method on a ref-param container field is a write to that
// field, so it must obey the `changes` set — `b.items.push(v)` outside `changes b.other` is a
// violation. (The receiver is a value, not a `mutable T&` arg, so it bypassed checkFrameMutableRefArg.)
func TestChangesBuiltinMethodOutOfSetErrors(t *testing.T) {
	src := `
struct Buf:
    items: mutable darray[i64]
    other: mutable darray[i64]

def bad(b: mutable Buf&) changes b.other:
    b.items.push(7)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "frame_method_out.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "outside the `changes` set") {
		t.Fatalf("`b.items.push` outside `changes b.other` must error, got:\n%s", allDiagnostics(result))
	}
}

// The same call INSIDE the declared set is clean — the fix enforces the frame, it does not forbid the
// write outright.
func TestChangesBuiltinMethodInSetIsClean(t *testing.T) {
	src := `
struct Buf:
    items: mutable darray[i64]
    other: mutable darray[i64]

def good(b: mutable Buf&) changes b.items:
    b.items.push(7)
`
	result := analyzeTreeTestSource(t, "frame_method_in.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("`b.items.push` within `changes b.items` should be clean, got: %v", errs)
	}
}

// A mutating builtin method on a `preserves`d field is a violation (the write touches preserved state).
func TestPreservesBuiltinMethodErrors(t *testing.T) {
	src := `
struct Buf:
    items: mutable darray[i64]
    other: mutable darray[i64]

def bad(b: mutable Buf&) changes b preserves b.items:
    b.items.push(7)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "frame_method_pres.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "preserves") {
		t.Fatalf("`b.items.push` on a preserved field must error, got:\n%s", allDiagnostics(result))
	}
}

// Sibling carrier: the same rule covers dict mutators (`d.put`) on a ref-param field.
func TestChangesDictMethodOutOfSetErrors(t *testing.T) {
	src := `
struct Tab:
    a: mutable dict[i64, i64]
    b: mutable dict[i64, i64]

def bad(t: mutable Tab&) changes t.b:
    t.a.put(1, 2)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "frame_dict_out.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "outside the `changes` set") {
		t.Fatalf("`t.a.put` outside `changes t.b` must error, got:\n%s", allDiagnostics(result))
	}
}

// AUDIT (cluster C, frame channel): a write laundered through a borrow-local alias to a place OUTSIDE
// the `changes` set is a frame violation — `h := &r.health; h <- 999` in a function declared
// `changes r.px` writes r.health. The frame check now resolves the local to the place it borrows.
func TestChangesBorrowLocalAliasOutOfSetErrors(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    health: mutable i32

def bad(r: mutable Render&) changes r.px:
    h = &r.health
    h <- 999
    r.px <- 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "frame_alias_out.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "outside the `changes` set") {
		t.Fatalf("`h <- 999` (h = &r.health) outside `changes r.px` must error, got:\n%s", allDiagnostics(result))
	}
}

// The same alias write to an IN-SET field stays clean — the resolution is exact (path-level), not a
// blanket rejection of all alias writes.
func TestChangesBorrowLocalAliasInSetIsClean(t *testing.T) {
	src := `
struct Render:
    px: mutable i32
    health: mutable i32

def good(r: mutable Render&) changes r.px:
    h = &r.px
    h <- 7
`
	result := analyzeTreeTestSource(t, "frame_alias_in.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a write through an alias to the in-set field r.px should be clean, got: %v", errs)
	}
}
