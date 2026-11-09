package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

// An added module member whose signature names a module type records a resolved type name;
// the update follows that growth instead of falling back.
func TestRetainedAnalysisAcceptsAddedMemberNamingModuleType(t *testing.T) {
	options := AnalyzeOptions{}
	before := "module Shapes:\n    struct Box:\n        w: i64\n\n    def area(b: Box&) -> i64:\n        return b.w\n"
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	edited := before + "\n    def double_area(b: Box&) -> i64:\n        return b.w + b.w\n"
	updated, err := retained.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
	if err != nil {
		t.Fatalf("added member fell back: %v", err)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, edited), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

const retainedUsingBefore = `module Geo:
    struct Point:
        x: i64

    def origin() -> Point:
        return Point{x: 0}

module App:
    def width(p: Geo::Point&) -> i64:
        return p.x

    def far() -> i64:
        q: Point = origin()
        return q.x

    def unrelated(n: i64) -> i64:
        return n + 1
`

// A top-level `using Geo` line resolves the bare names in far; removing it unresolves them again.
// Both directions must match a fresh analysis without falling back.
func TestRetainedAnalysisAcceptsUsingEdits(t *testing.T) {
	options := AnalyzeOptions{}
	with := strings.Replace(retainedUsingBefore, "module App:\n", "using Geo\n\nmodule App:\n", 1)
	if retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, retainedUsingBefore), options)) == "" ||
		retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, with), options)) != "" {
		t.Fatalf("fixture must fail without the using and pass with it")
	}
	for _, step := range []struct{ name, before, after string }{
		{"add", retainedUsingBefore, with},
		{"remove", with, retainedUsingBefore},
	} {
		_, retained := AnalyzeRetained(retainedParse(t, step.before), retainedParse(t, step.before), []byte(step.before), options)
		updated, err := retained.Update(retainedParse(t, step.after), retainedParse(t, step.after), []byte(step.after))
		if err != nil {
			t.Fatalf("%s: using edit fell back: %v", step.name, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, step.after), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("%s: diverged\n got:\n%s\nwant:\n%s", step.name, got, want)
		}
		if retained.LastStats.Changed == 0 {
			t.Fatalf("%s: no function was re-analyzed: %+v", step.name, retained.LastStats)
		}
	}
}

// A using re-analyzes only the functions it can change: App.helper shadows Geo.helper (lexical
// names come before every using), and `Shape.area(...)` names a variant, not a UFCS call.
func TestRetainedAnalysisUsingSkipsShadowedMembers(t *testing.T) {
	options := AnalyzeOptions{}
	before := `module Geo:
    def helper() -> i64:
        return 1

    def area() -> i64:
        return 2

enum Shape:
    area(i64)
    none

module App:
    def helper() -> i64:
        return 3

    def uses_helper() -> i64:
        return helper()

    def uses_variant() -> Shape:
        return Shape.area(4)

    def uses_area() -> i64:
        return area()
`
	with := strings.Replace(before, "module App:\n", "using Geo\n\nmodule App:\n", 1)
	if retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, before), options)) == "" ||
		retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, with), options)) != "" {
		t.Fatalf("fixture must fail without the using and pass with it:\n%s", retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, with), options)))
	}
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	for step, src := range []string{with, before} {
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("step %d: using edit fell back: %v", step, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d: diverged\n got:\n%s\nwant:\n%s", step, got, want)
		}
		if retained.LastStats.Changed != 1 {
			t.Fatalf("step %d: want only uses_area re-analyzed: %+v", step, retained.LastStats)
		}
	}
}

// The add, its revert and a later member addition run on one retained analysis, as an
// editor's undo does.
func TestRetainedAnalysisRevertsUsingEditInPlace(t *testing.T) {
	options := AnalyzeOptions{}
	with := strings.Replace(retainedUsingBefore, "module App:\n", "using Geo\n\nmodule App:\n", 1)
	_, retained := AnalyzeRetained(retainedParse(t, retainedUsingBefore), retainedParse(t, retainedUsingBefore), []byte(retainedUsingBefore), options)
	// The last step adds a member to App after the using line moved it: its live block is
	// found by structure, not by its stale position.
	for step, src := range []string{with, retainedUsingBefore, with, with + "\n    def extra() -> i64:\n        return origin().x\n"} {
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("step %d: using edit fell back: %v", step, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d: diverged\n got:\n%s\nwant:\n%s", step, got, want)
		}
	}
}

// Two blocks of one module carry different `using` lines: a function added to the second
// takes that block's usings, not the first block's.
func TestRetainedAnalysisAddedMemberTakesItsBlockUsings(t *testing.T) {
	options := AnalyzeOptions{}
	before := `module App:
    def first() -> i64:
        return 1

    module Inner:
        struct Spot:
            x: i64

extend App:
    using Inner

    def second() -> i64:
        return 2
`
	edited := before + "\n    def third() -> i64:\n        s: Spot = Spot{x: 3}\n        return s.x\n"
	if got := retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, edited), options)); got != "" {
		t.Fatalf("fixture must analyze cleanly, got:\n%s", got)
	}
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	updated, err := retained.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
	if err != nil {
		t.Fatalf("added member fell back: %v", err)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, edited), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

const retainedHierarchyBefore = `global region src

enum Node layout(handle: u32) @src:
    pass

enum Expr is Node:
    Lit(i64)
    Neg(Expr)

enum Stmt is Node:
    Ret(Expr)
    Skip

def eval(e: Expr) -> i64:
    match e:
        Expr.Lit(v):
            return v
        Expr.Neg(x):
            return 0 - eval(x)

def first(s: Stmt) -> i64:
    match s:
        Stmt.Ret(e):
            return 1
        Stmt.Skip:
            return 0

def pass_through(e: Expr) -> Expr:
    return e

def is_lit(e: Expr) -> bool:
    match e:
        Expr.Lit(_):
            return true
        _:
            return false

def make() -> Expr:
    return Expr.Lit(1)
`

// A variant added to a refinement of a store-backed hierarchy shifts every later leaf tag
// (Stmt's) and makes eval's match non-exhaustive. Only eval's match can change: is_lit's has a
// catch-all arm and make only constructs.
func TestRetainedAnalysisAcceptsHierarchyVariantEdits(t *testing.T) {
	options := AnalyzeOptions{}
	with := strings.Replace(retainedHierarchyBefore, "    Neg(Expr)\n", "    Neg(Expr)\n    Zero\n", 1)
	if retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, with), options)) == "" {
		t.Fatalf("fixture must report the non-exhaustive match")
	}
	for _, step := range []struct{ name, before, after string }{
		{"add", retainedHierarchyBefore, with},
		{"remove", with, retainedHierarchyBefore},
	} {
		_, retained := AnalyzeRetained(retainedParse(t, step.before), retainedParse(t, step.before), []byte(step.before), options)
		updated, err := retained.Update(retainedParse(t, step.after), retainedParse(t, step.after), []byte(step.after))
		if err != nil {
			t.Fatalf("%s: hierarchy edit fell back: %v", step.name, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, step.after), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("%s: diverged\n got:\n%s\nwant:\n%s", step.name, got, want)
		}
		if got := retained.LastStats.EnumUsers; got != 1 {
			t.Fatalf("%s: re-analyzed %d enum users, want 1 (eval)", step.name, got)
		}
	}
}

// A recursive function's re-analyzed body must converge its own summary first, as a fresh
// analysis does; reading the summary the update invalidated made the comprehension copy
// `base` look backed by `taint`.
func TestRetainedAnalysisReconvergesRecursiveBodyEdit(t *testing.T) {
	options := AnalyzeOptions{}
	before := `def f(body: darray[i64]&, taint: mutable darray[sview]&, regions: mutable darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        for s in body |taint, regions|:
            base: darray[sview] = [n for n in taint]
            nested: mutable darray[sview] = [n for n in taint]
            nested_regions: mutable darray[sview] = [r for r in regions]
            f(body, nested, nested_regions)
            for name in base |taint, regions|:
                taint.push(name)
                regions.push(name)
`
	edited := strings.Replace(before, "            for name in base", "            _ = base.count\n            for name in base", 1)
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	updated, err := retained.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
	if err != nil {
		t.Fatalf("body edit fell back: %v", err)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, edited), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

// An effect added to a function's row and then removed must leave its callers' inferred
// rows too, including around a call cycle, whose rows otherwise keep supporting each other.
func TestRetainedAnalysisRetractsEffectThroughCallCycle(t *testing.T) {
	options := AnalyzeOptions{}
	before := `def leaf(n: i64) -> i64 can[Abort.Panic]:
    return n + 1

def ping(n: i64) -> i64:
    return 0 if n <= 0 else pong(n - 1) + leaf(n)

def pong(n: i64) -> i64:
    return 0 if n <= 0 else ping(n - 1)

def top(n: i64) -> i64:
    return pong(n)
`
	edited := strings.Replace(before, "-> i64 can[Abort.Panic]:", "-> i64 can[Abort.Panic, Console.Write]:", 1)
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	for step, src := range []string{edited, before, edited, before} {
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("step %d fell back: %v", step, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d diverged\n got:\n%s\nwant:\n%s", step, got, want)
		}
	}
}

// rec calls itself and is re-analyzed whenever leaf's signature changes; its recursive call
// must read rec's declared row, as in a fresh analysis, not the row the last solve left.
func TestRetainedAnalysisRetractsEffectThroughSelfRecursiveCaller(t *testing.T) {
	options := AnalyzeOptions{}
	before := `def leaf(n: i64) -> i64 can[Abort.Panic]:
    return n + 1

def rec(n: i64) -> i64:
    return 0 if n <= 0 else rec(n - 1) + leaf(n)

def top(n: i64) -> i64:
    can Abort.Panic:
        return rec(n)
`
	edited := strings.Replace(before, "-> i64 can[Abort.Panic]:", "-> i64 can[Abort.Panic, Console.Write]:", 1)
	if retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, edited), options)) == retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, before), options)) {
		t.Fatal("fixture edit does not change the diagnostics")
	}
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	for step, src := range []string{edited, before, edited, before} {
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("step %d fell back: %v", step, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d diverged\n got:\n%s\nwant:\n%s", step, got, want)
		}
	}
}

const retainedBorrowBefore = `struct BorrowedBox:
    value: i64&

global stable: i64 = 2

def pack(value: i64&) -> BorrowedBox:
    return BorrowedBox{value: value}

def escapes_local() -> BorrowedBox:
    local: i64 = 3
    return pack(&local)

def keeps_param(value: i64&) -> BorrowedBox:
    return pack(value)
`

// pack stops returning its parameter: the unchanged caller's escape error must
// go, which only happens if the caller's return-borrow results are recomputed.
var retainedBorrowAfter = strings.Replace(retainedBorrowBefore, "return BorrowedBox{value: value}", "return BorrowedBox{value: &stable}", 1)

func TestRetainedAnalysisRecomputesCallersOfChangedReturnBorrowSummaries(t *testing.T) {
	options := AnalyzeOptions{}
	before, retained := AnalyzeRetained(retainedParse(t, retainedBorrowBefore), retainedParse(t, retainedBorrowBefore), []byte(retainedBorrowBefore), options)
	if !strings.Contains(retainedDiagnostics(before), "function-local storage") {
		t.Fatalf("fixture lost its escape error:\n%s", retainedDiagnostics(before))
	}
	if strings.Contains(retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, retainedBorrowAfter), options)), "function-local storage") {
		t.Fatal("fixture edit does not change the caller's diagnostics")
	}
	for i, src := range []string{retainedBorrowAfter, retainedBorrowBefore, retainedBorrowAfter} {
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("step %d fell back: %v", i, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d diverged\n got:\n%s\nwant:\n%s", i, got, want)
		}
	}
}

// Region-parameter inference reads bodies during the declaration phase: making
// a body grow its container parameter changes the function's signature. Such an
// edit must not be applied as a body-only change.
func TestRetainedAnalysisRejectsBodyEditsThatChangeDeclarationResults(t *testing.T) {
	before := `def fill(xs: mutable darray[i64]&) -> void:
    n: i64 = xs.count.i64()

def caller() -> void:
    items: mutable darray[i64] = []
    fill(items)
`
	after := strings.Replace(before, "    n: i64 = xs.count.i64()", "    xs.push(1)", 1)
	options := AnalyzeOptions{}
	header := func(src string) string {
		file := retainedParse(t, src)
		AnalyzeWithOptions(file, options)
		return retainedHeader(file.Decls[0].(*ast.FuncDecl))
	}
	if header(before) == header(after) {
		t.Skip("fixture no longer changes the inferred signature")
	}
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	_, err := retained.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err == nil || !strings.Contains(err.Error(), "declaration") {
		t.Fatalf("err = %v, want a declaration-phase fallback", err)
	}
}

// Adding a helper and calling it from an existing body is the most common edit;
// it must update in place and agree with a fresh analysis.
func TestRetainedAnalysisAcceptsAddedFunction(t *testing.T) {
	after := strings.Replace(retainedBefore, "def caller(x: i64) -> i64:\n    return helper(x)",
		"def extra(z: i64) -> i64:\n    v: mutable i64 = z\n    base: uintptr = (&v).cast[uintptr] can Unsafe.PointerCast\n    return z + 1\n\ndef caller(x: i64) -> i64:\n    return extra(helper(x))", 1)
	if after == retainedBefore {
		t.Fatal("fixture edit did not apply")
	}
	options := AnalyzeOptions{}
	_, retained := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), options)
	if retained == nil {
		t.Fatal("analysis was not retained")
	}
	updated, err := retained.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatalf("adding a function fell back: %v", err)
	}
	if retained.LastStats.Added != 1 {
		t.Fatalf("stats = %+v, want 1 added", retained.LastStats)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, after), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("retained update disagrees with a fresh analysis\nretained:\n%s\nfresh:\n%s", got, want)
	}
	if !strings.Contains(retainedDiagnostics(updated), `call to "extra" requires can[Unsafe]`) {
		t.Fatalf("the added function's effects did not reach its caller:\n%s", retainedDiagnostics(updated))
	}
	// A later body edit still works against the extended retained state.
	again := strings.Replace(after, "return z + 1", "return z + 2", 1)
	if _, err := retained.Update(retainedParse(t, again), retainedParse(t, again), []byte(again)); err != nil {
		t.Fatalf("body edit after an added function fell back: %v", err)
	}
}

// Removing a function updates in place; a caller left mentioning it, though its
// own text is unchanged, must report the now-undefined call like a fresh analysis.
func TestRetainedAnalysisAcceptsRemovedFunction(t *testing.T) {
	options := AnalyzeOptions{}
	_, retained := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), options)
	if retained == nil {
		t.Fatal("analysis was not retained")
	}
	steps := []string{
		// later stops calling helper, then helper is removed while caller still calls it.
		strings.Replace(retainedBefore, "def later(y: i64) -> i64:\n    return helper(y)", "def later(y: i64) -> i64:\n    return y", 1),
	}
	steps = append(steps, strings.Replace(steps[0], "def helper(x: i64) -> i64:\n    v: mutable i64 = x\n    base: uintptr = (&v).cast[uintptr] can Unsafe.PointerCast\n    return x\n\n", "", 1))
	for i, src := range steps {
		if i > 0 && src == steps[i-1] {
			t.Fatalf("step %d edit did not apply", i)
		}
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("step %d fell back: %v", i, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d disagrees with a fresh analysis\nretained:\n%s\nfresh:\n%s", i, got, want)
		}
	}
	if retained.LastStats.Removed != 1 {
		t.Fatalf("stats = %+v, want 1 removed", retained.LastStats)
	}
}

const retainedModuleBefore = `
module Tool:
    public:
        def base(x: i64) -> i64:
            return x

extend Tool:
    private:
        def twice(x: i64) -> i64:
            return x + x
    public:
        def run(x: i64) -> i64:
            return twice(base(x))

def outside(x: i64) -> i64:
    return Tool::run(x)
`
