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
