package semantic

import (
	"testing"

	"elisacore/src/ast"
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

// docs/85 §4 Stage 4: an effect law forbids effects; a conforming function that uses none is clean.
func TestEffectLawFulfillsClean(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate

def pure_add(x: i64, y: i64) -> i64 fulfills NoAlloc:
    return x + y
`
	result := analyzeTreeTestSource(t, "effect_law_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a function using no forbidden effect should fulfill the effect law cleanly, got: %v", errs)
	}
}

// The effect law is discharged against the inferred effect set: a function that allocates while
// `fulfills NoAlloc` is a compile error.
func TestEffectLawFulfillsViolationErrors(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate

def grow() -> i64 fulfills NoAlloc:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(7)
        return xs[0]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "effect_law_violation.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "uses the `Memory.Allocate` effect") {
		t.Fatalf("allocating under `fulfills NoAlloc` must error, got:\n%s", allDiagnostics(result))
	}
}

// Applying an effect law with a subject (`fulfills f is NoAlloc`) is a wrong-form error — effect
// laws are function-level.
func TestEffectLawWithSubjectErrors(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate

def f(x: i64) -> i64 fulfills x is NoAlloc:
    return x
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "effect_law_subject.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "constrains the whole function") {
		t.Fatalf("applying an effect law with a subject must error, got:\n%s", allDiagnostics(result))
	}
}

// An effect law used in value `is` position is a wrong-class error.
func TestEffectLawInValueIsErrors(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate

def f(x: i64) -> bool:
    return x is NoAlloc
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "effect_law_value_is.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is an effect law") {
		t.Fatalf("using an effect law with value `is` must error, got:\n%s", allDiagnostics(result))
	}
}

// docs/89 Stage 4 (shape): a function whose every index access is statically proven in-bounds
// fulfills the built-in `NoBoundsChecks` shape law cleanly.
func TestShapeLawNoBoundsChecksClean(t *testing.T) {
	src := `
def sum(xs: darray[i64]) -> i64 fulfills NoBoundsChecks:
    total: mutable i64 = 0
    for i in 0..<xs.count:
        total <- total + xs[i]
    return total
`
	result := analyzeTreeTestSource(t, "shape_nobounds_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a function with only proven index accesses should fulfill NoBoundsChecks, got: %v", errs)
	}
}

// An unproven, bounds-requiring index access would emit a runtime bounds check → violates the law.
func TestShapeLawNoBoundsChecksViolationErrors(t *testing.T) {
	src := `
def third(xs: darray[i64]) -> i64 fulfills NoBoundsChecks:
    return xs[2]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "shape_nobounds_violation.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "fulfills NoBoundsChecks") {
		t.Fatalf("an unproven index access under `fulfills NoBoundsChecks` must error, got:\n%s", allDiagnostics(result))
	}
}

// Applying a shape law with a subject is a wrong-form error — shape laws are function-level.
func TestShapeLawWithSubjectErrors(t *testing.T) {
	src := `
def f(xs: darray[i64]) -> i64 fulfills xs is NoBoundsChecks:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "shape_subject.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "constrains the whole function") {
		t.Fatalf("applying a shape law with a subject must error, got:\n%s", allDiagnostics(result))
	}
}

// A shape law used in value `is` position is a wrong-class error.
func TestShapeLawInValueIsErrors(t *testing.T) {
	src := `
def f(xs: darray[i64]) -> bool:
    return xs is NoBoundsChecks
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "shape_value_is.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is a shape law") {
		t.Fatalf("using a shape law with value `is` must error, got:\n%s", allDiagnostics(result))
	}
}

// docs/85 §6: a composite law unions its members — a function clean against every member fulfills it.
func TestCompositeLawFulfillsClean(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate
law NoPanic forbids Abort.Panic
law Leaf includes NoAlloc, NoPanic

def add(x: i64, y: i64) -> i64 fulfills Leaf:
    return x + y
`
	result := analyzeTreeTestSource(t, "composite_clean.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a function clean against all composite members should fulfill it, got: %v", errs)
	}
}

// A composite law is violated if the function uses an effect any member forbids (here Memory.Allocate
// via the included NoAlloc).
func TestCompositeLawFulfillsViolationErrors(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate
law NoPanic forbids Abort.Panic
law Leaf includes NoAlloc, NoPanic

def grow() -> i64 fulfills Leaf:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(7)
        return xs[0]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "composite_violation.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "fulfills Leaf") || !contains(allDiagnostics(result), "Memory.Allocate") {
		t.Fatalf("a composite member's forbidden effect must be enforced, got:\n%s", allDiagnostics(result))
	}
}

// A composite may union an effect law and a built-in shape law; both obligations are discharged.
func TestCompositeLawMixesEffectAndShape(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate
law HotKernel includes NoAlloc, NoBoundsChecks

def get_third(xs: darray[i64]) -> i64 fulfills HotKernel:
    return xs[2]
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "composite_shape.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "requires NoBoundsChecks") {
		t.Fatalf("a composite's shape member must be discharged, got:\n%s", allDiagnostics(result))
	}
}

// A composite that includes a value law is malformed — `includes` composes only function-level laws.
func TestCompositeLawRejectsValueMember(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0
law Bad includes Positive

def f() -> i64 fulfills Bad:
    return 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "composite_value_member.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "composes only function-level laws") {
		t.Fatalf("a composite including a value law must error, got:\n%s", allDiagnostics(result))
	}
}

// A cyclic include graph is rejected.
func TestCompositeLawRejectsCycle(t *testing.T) {
	src := `
law A includes B
law B includes A

def f() -> i64 fulfills A:
    return 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "composite_cycle.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "cyclic") {
		t.Fatalf("a cyclic composite must error, got:\n%s", allDiagnostics(result))
	}
}

// docs/89 Stage 5 (measure): `fulfills Vectorizes` compiles with no static obligation and tags the
// function's range loops expected-to-vectorize, opting them into the post-codegen autovec verifier.
func TestMeasureLawVectorizesTagsLoops(t *testing.T) {
	src := `
def scale(xs: darray[i64]) -> i64 fulfills Vectorizes:
    total: mutable i64 = 0
    for i in 0..<xs.count:
        total <- total + xs[i] * 2
    return total
`
	result := analyzeTreeTestSource(t, "measure_vectorizes.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a measure law is verify-only and should compile cleanly, got: %v", errs)
	}
	fn, ok := result.File.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected a FuncDecl, got %T", result.File.Decls[0])
	}
	for _, stmt := range fn.Body {
		if forStmt, ok := stmt.(*ast.ForStmt); ok {
			if !forStmt.AutovecExpected {
				t.Fatalf("a range loop in a `fulfills Vectorizes` function must be tagged AutovecExpected")
			}
			return
		}
	}
	t.Fatalf("did not find the range loop to check its AutovecExpected tag")
}

// Applying a measure law with a subject is a wrong-form error.
func TestMeasureLawWithSubjectErrors(t *testing.T) {
	src := `
def f(xs: darray[i64]) -> i64 fulfills xs is Vectorizes:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "measure_subject.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "constrains the whole function") {
		t.Fatalf("applying a measure law with a subject must error, got:\n%s", allDiagnostics(result))
	}
}

// A measure law used in value `is` position is a wrong-class error.
func TestMeasureLawInValueIsErrors(t *testing.T) {
	src := `
def f(x: i64) -> bool:
    return x is Vectorizes
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "measure_value_is.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "is a measure law") {
		t.Fatalf("using a measure law with value `is` must error, got:\n%s", allDiagnostics(result))
	}
}

// docs/85 §8 hard rule: a measure law is NOT composable — including it in a law is a class error.
func TestMeasureLawNotComposable(t *testing.T) {
	src := `
law NoAlloc forbids Memory.Allocate
law Bad includes NoAlloc, Vectorizes

def f() -> i64 fulfills Bad:
    return 1
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "measure_composable.elisa", src, AnalyzeOptions{})
	if !contains(allDiagnostics(result), "not composable") {
		t.Fatalf("including a measure law in a composite must error, got:\n%s", allDiagnostics(result))
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
