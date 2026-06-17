package semantic

import "testing"

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
