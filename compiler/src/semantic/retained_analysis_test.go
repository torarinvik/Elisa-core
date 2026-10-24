package semantic

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func retainedParse(t *testing.T, src string) *ast.File {
	t.Helper()
	l := lexer.New("retained.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("retained.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return file
}

func retainedDiagnostics(r *Result) string {
	var out []string
	for _, d := range r.Diagnostics {
		out = append(out, d.String())
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

const retainedBefore = `
def helper(x: i64) -> i64:
    v: mutable i64 = x
    base: uintptr = (&v).cast[uintptr] can Unsafe.PointerCast
    return x

def caller(x: i64) -> i64:
    return helper(x)

def later(y: i64) -> i64:
    return helper(y)
`

// Same signatures; helper's body loses its unsafe cast and grows a line, which
// changes the callers' effect warnings and shifts everything below it.
const retainedAfter = `
def helper(x: i64) -> i64:
    v: i64 = x
    w: i64 = v
    u: i64 = w
    return u

def caller(x: i64) -> i64:
    return helper(x)

def later(y: i64) -> i64:
    return helper(y)
`

func TestRetainedAnalysisUpdateMatchesFreshAnalysis(t *testing.T) {
	options := AnalyzeOptions{}
	before, retained := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), options)
	if retained == nil {
		t.Fatal("analysis was not retained")
	}
	if !strings.Contains(retainedDiagnostics(before), `call to "helper" requires can[Unsafe]`) {
		t.Fatalf("fixture lost its cross-function warning:\n%s", retainedDiagnostics(before))
	}
	updated, err := retained.Update(retainedParse(t, retainedAfter), retainedParse(t, retainedAfter), []byte(retainedAfter))
	if err != nil {
		t.Fatalf("body-only edit fell back: %v", err)
	}
	if retained.LastStats.Changed != 1 || retained.LastStats.Shifted != 2 {
		t.Fatalf("stats = %+v, want 1 changed and 2 shifted", retained.LastStats)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, retainedAfter), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("retained update diverged from a fresh analysis\n got:\n%s\nwant:\n%s", got, want)
	}
	// And back again: the retained state keeps tracking the latest source.
	reverted, err := retained.Update(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore))
	if err != nil {
		t.Fatalf("revert fell back: %v", err)
	}
	if got, want := retainedDiagnostics(reverted), retainedDiagnostics(before); got != want {
		t.Fatalf("revert diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRetainedAnalysisAcceptsSignatureEdits(t *testing.T) {
	options := AnalyzeOptions{}
	_, retained := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), options)
	// A caller's own signature, then a callee's parameter type its callers no longer match.
	steps := []string{
		strings.Replace(retainedBefore, "def caller(x: i64) -> i64:", "def caller(x: i32) -> i64:", 1),
	}
	steps = append(steps, strings.Replace(steps[0], "def helper(x: i64) -> i64:", "def helper(x: bool) -> i64:", 1))
	for i, src := range steps {
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("signature edit %d fell back: %v", i, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("signature edit %d disagrees with a fresh analysis\nretained:\n%s\nfresh:\n%s", i, got, want)
		}
		if i == 1 && len(fresh.Errors()) == 0 {
			t.Fatal("fixture: the callee's new parameter type must reject its callers")
		}
	}
	added := steps[1] + "\nstruct Extra[T]:\n    v: T\n"
	if _, err := retained.Update(retainedParse(t, added), retainedParse(t, added), []byte(added)); err == nil {
		t.Fatal("adding a generic struct must fall back")
	}
}

// Adding a defaulted parameter types its default in the declaration phase; that is the edited
// function's own header, not a program-wide change, and callers that omit it still check.
func TestRetainedAnalysisAcceptsDefaultedParameterEdits(t *testing.T) {
	options := AnalyzeOptions{}
	_, retained := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), options)
	steps := []string{
		strings.Replace(retainedBefore, "def helper(x: i64) -> i64:", "def helper(x: i64, y: i64 = 0) -> i64:", 1),
	}
	steps = append(steps, strings.Replace(steps[0], "y: i64 = 0)", "y: i64 = 0, z: bool = true)", 1))
	steps = append(steps, strings.Replace(steps[1], "z: bool = true)", "z: bool = 1)", 1))
	for i, src := range steps {
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if err != nil {
			// A default the declaration phase rejects changes its diagnostics: falling back is right.
			if i == 2 {
				continue
			}
			t.Fatalf("defaulted parameter edit %d fell back: %v", i, err)
		}
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("defaulted parameter edit %d disagrees with a fresh analysis\nretained:\n%s\nfresh:\n%s", i, got, want)
		}
	}
}

func TestRetainedAnalysisReanalyzesFunctionsWhoseLinesMoved(t *testing.T) {
	options := AnalyzeOptions{}
	_, retained := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), options)
	// A comment inside the last function moves only its own statement.
	edited := strings.Replace(retainedBefore, "def later(y: i64) -> i64:\n", "def later(y: i64) -> i64:\n    # moved\n", 1)
	updated, err := retained.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
	if err != nil {
		t.Fatalf("comment edit fell back: %v", err)
	}
	if retained.LastStats.Changed != 1 {
		t.Fatalf("stats = %+v, want the commented function re-analyzed", retained.LastStats)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, edited), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

// Comment lines above the first declaration shift every declaration below them and change
// no declaration's text: a plain shift, not a full analysis.
func TestRetainedAnalysisAcceptsLeadingCommentLines(t *testing.T) {
	options := AnalyzeOptions{}
	_, retained := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), options)
	edited := "# leading\n# comment\n\n" + retainedBefore
	updated, err := retained.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
	if err != nil {
		t.Fatalf("leading comment edit fell back: %v", err)
	}
	if retained.LastStats.Changed != 0 {
		t.Fatalf("stats = %+v, want only shifted declarations", retained.LastStats)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, edited), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

const retainedRegionsBefore = "#line 1 a.elisa\n" +
	"def first(x: i64) -> i64:\n    return x\n\n" +
	"#line 1 b.elisa\n" +
	"# b's header\n\ndef second(y: i64) -> i64:\n    return first(y)\n\n" +
	"def third(z: i64) -> i64:\n    return second(z)\n"

// A comment added to the header of an included file's region shifts that region only. The
// span of the previous region's last declaration ends at the directive, so it is unchanged.
func TestRetainedAnalysisAcceptsRegionHeaderCommentLines(t *testing.T) {
	options := AnalyzeOptions{}
	before := retainedRegionsBefore
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	edited := strings.Replace(before, "# b's header\n", "# b's header\n# more header\n", 1)
	updated, err := retained.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
	if err != nil {
		t.Fatalf("region header comment fell back: %v", err)
	}
	if retained.LastStats.Changed != 0 {
		t.Fatalf("stats = %+v, want only shifted declarations", retained.LastStats)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, edited), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

const retainedConstBefore = `
const LIMIT: i64 = 1

def reads() -> i64:
    xs: i64[3] = [1, 2, 3]
    return xs[LIMIT]

def narrows() -> u8:
    x: u8 = LIMIT
    return x

def other(y: i64) -> i64:
    return y
`

// A typed const's new value is re-evaluated and its users re-analyzed: 300 is out of bounds and
// does not fit in u8, and going back to 1 clears both, exactly as a fresh analysis reports.
func TestRetainedAnalysisAcceptsConstValueEdits(t *testing.T) {
	options := AnalyzeOptions{}
	_, retained := AnalyzeRetained(retainedParse(t, retainedConstBefore), retainedParse(t, retainedConstBefore), []byte(retainedConstBefore), options)
	for i, value := range []string{"300", "2", "1"} {
		src := strings.Replace(retainedConstBefore, "LIMIT: i64 = 1", "LIMIT: i64 = "+value, 1)
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("const edit %d fell back: %v", i, err)
		}
		if retained.LastStats.ConstUsers != 2 {
			t.Fatalf("const edit %d: stats = %+v, want two users re-analyzed", i, retained.LastStats)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh)
		if got != want {
			t.Fatalf("const edit %d diverged\n got:\n%s\nwant:\n%s", i, got, want)
		}
		if i == 0 && (!strings.Contains(want, "out of bounds") || !strings.Contains(want, "does not fit")) {
			t.Fatalf("const edit 0 should report both value errors:\n%s", want)
		}
	}
}

// An added typed const is declared and evaluated, and the functions that named it before it
// existed are re-analyzed: their undefined-name errors go, as in a fresh analysis.
func TestRetainedAnalysisAcceptsAddedConst(t *testing.T) {
	options := AnalyzeOptions{}
	fns := "def reads() -> i64:\n    return LIMIT\n\ndef other(y: i64) -> i64:\n    return y\n"
	before := "const A: i64 = 1\n\nconst B: i64 = 2\n\n" + fns
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	if !strings.Contains(retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, before), options)), "LIMIT") {
		t.Fatal("the unadded const should be reported")
	}
	// Added between two existing consts, so pairing must go by name.
	edited := "const A: i64 = 1\n\nconst LIMIT: i64 = 3\n\nconst B: i64 = 2\n\n" + fns
	updated, err := retained.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
	if err != nil {
		t.Fatalf("added const fell back: %v", err)
	}
	if retained.LastStats.ConstUsers != 1 {
		t.Fatalf("stats = %+v, want one user re-analyzed", retained.LastStats)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, edited), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("diverged\n got:\n%s\nwant:\n%s", got, want)
	}
	declared := 0
	for _, sd := range retained.decls {
		if cd, ok := sd.Decl.(*ast.ConstDecl); ok && cd.Name == "LIMIT" {
			declared++
		}
	}
	if declared != 1 {
		t.Fatalf("the added const is declared %d times", declared)
	}
	// A value edit to the added const then works like any other, and removing it again
	// brings back the undefined-name error.
	again := strings.Replace(edited, "= 3", "= 4", 1)
	if _, err := retained.Update(retainedParse(t, again), retainedParse(t, again), []byte(again)); err != nil {
		t.Fatalf("value edit after the add fell back: %v", err)
	}
	updated, err = retained.Update(retainedParse(t, before), retainedParse(t, before), []byte(before))
	if err != nil {
		t.Fatalf("removing the const fell back: %v", err)
	}
	fresh = AnalyzeWithOptions(retainedParse(t, before), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want || !strings.Contains(got, "LIMIT") {
		t.Fatalf("removal diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

const retainedEnumBefore = `
enum Shape:
    Circle(i64)
    Square(i64)

def area(s: Shape) -> i64:
    match s:
        Shape.Circle(r):
            return r * r * 3
        Shape.Square(w):
            return w * w

def other(y: i64) -> i64:
    return y
`

// A new variant makes the match non-exhaustive, a payload-less one is also a const value, and
// removing them restores the program; each step matches a fresh analysis.
func TestRetainedAnalysisAcceptsPlainEnumEdits(t *testing.T) {
	options := AnalyzeOptions{}
	_, retained := AnalyzeRetained(retainedParse(t, retainedEnumBefore), retainedParse(t, retainedEnumBefore), []byte(retainedEnumBefore), options)
	withTri := strings.Replace(retainedEnumBefore, "    Square(i64)\n", "    Square(i64)\n    Triangle(i64, i64)\n", 1)
	withNone := strings.Replace(withTri, "    Triangle(i64, i64)\n", "    Triangle(i64, i64)\n    Empty\n", 1)
	for i, src := range []string{withTri, withNone, withTri, retainedEnumBefore} {
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("enum edit %d fell back: %v", i, err)
		}
		if retained.LastStats.EnumUsers != 1 {
			t.Fatalf("enum edit %d: stats = %+v, want one user re-analyzed", i, retained.LastStats)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh)
		if got != want {
			t.Fatalf("enum edit %d diverged\n got:\n%s\nwant:\n%s", i, got, want)
		}
		if i == 0 && len(fresh.Errors()) == 0 {
			t.Fatalf("fixture: the added variant must make the match non-exhaustive:\n%s", want)
		}
	}
}

// An added plain struct is declared, and the functions that named it before it existed are
// re-analyzed: their unknown-type errors go, as in a fresh analysis. Removing it is a fallback.
func TestRetainedAnalysisAcceptsAddedStruct(t *testing.T) {
	options := AnalyzeOptions{}
	fns := "def make() -> i64:\n    p: Point = Point{x: 1, y: 2}\n    return p.x + p.y\n\ndef other(y: i64) -> i64:\n    return y\n"
	before := "struct Keep:\n    v: i64\n\n" + fns
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	// Added before an existing struct, so pairing must go by name.
	edited := "struct Point:\n    x: i64\n    y: i64\n\nstruct Keep:\n    v: i64\n\n" + fns
	updated, err := retained.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
	if err != nil {
		t.Fatalf("added struct fell back: %v", err)
	}
	if retained.LastStats.ConstUsers != 1 {
		t.Fatalf("stats = %+v, want one user re-analyzed", retained.LastStats)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, edited), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("diverged\n got:\n%s\nwant:\n%s", got, want)
	}
	if len(fresh.Errors()) != 0 {
		t.Fatalf("fixture: the added struct should resolve every use:\n%s", retainedDiagnostics(fresh))
	}
	n := 0
	for _, sd := range retained.decls {
		if st, ok := sd.Decl.(*ast.StructDecl); ok && st.Name == "Point" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the added struct is declared %d times", n)
	}
	// Removing it again brings the unknown-type errors back.
	updated, err = retained.Update(retainedParse(t, before), retainedParse(t, before), []byte(before))
	if err != nil {
		t.Fatalf("removing the struct fell back: %v", err)
	}
	fresh = AnalyzeWithOptions(retainedParse(t, before), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want || len(fresh.Errors()) == 0 {
		t.Fatalf("removal diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

// An added global between two existing ones is declared, and its users lose their
// undefined-name errors, as in a fresh analysis.
func TestRetainedAnalysisAcceptsAddedGlobal(t *testing.T) {
	options := AnalyzeOptions{}
	fns := "def reads() -> i64:\n    return counter\n\ndef other(y: i64) -> i64:\n    return y\n"
	before := "global first: i64 = 1\nglobal last: i64 = 2\n\n" + fns
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	edited := "global first: i64 = 1\nglobal counter: i64 = 5\nglobal last: i64 = 2\n\n" + fns
	updated, err := retained.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
	if err != nil {
		t.Fatalf("added global fell back: %v", err)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, edited), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("diverged\n got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(retainedDiagnostics(fresh), "counter") {
		t.Fatalf("fixture: the added global should resolve its use:\n%s", retainedDiagnostics(fresh))
	}
	// Removing it again brings the undefined-name error back.
	updated, err = retained.Update(retainedParse(t, before), retainedParse(t, before), []byte(before))
	if err != nil {
		t.Fatalf("removing the global fell back: %v", err)
	}
	fresh = AnalyzeWithOptions(retainedParse(t, before), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want || !strings.Contains(got, "counter") {
		t.Fatalf("removal diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

// A struct that embeds the edited one by value changes layout with it, so a function that only
// uses the outer struct is re-analyzed too; one holding it through a reference is not.
func TestRetainedStructEditReachesByValueContainers(t *testing.T) {
	options := AnalyzeOptions{}
	before := "struct Inner:\n    a: i64\n\nstruct Outer:\n    inner: Inner\n\nstruct Far:\n    inner: Inner&\n\n" +
		"def outer_only(o: Outer&) -> i64:\n    return 1\n\ndef far_only(f: Far&) -> i64:\n    return 2\n\ndef none(y: i64) -> i64:\n    return y\n"
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	edited := strings.Replace(before, "    a: i64\n", "    a: i64\n    b: i64\n", 1)
	updated, err := retained.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited))
	if err != nil {
		t.Fatalf("struct edit fell back: %v", err)
	}
	if retained.LastStats.StructUsers != 1 {
		t.Fatalf("stats = %+v, want only outer_only re-analyzed", retained.LastStats)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, edited), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

// A struct edit that adds or clears a declaration-phase warning (padding) is followed in place:
// the warnings come from the edited program's declaration phase.
func TestRetainedAnalysisFollowsDeclarationWarningChanges(t *testing.T) {
	options := AnalyzeOptions{}
	before := "struct P:\n    a: i64\n    b: u8\n\ndef reads(p: P&) -> i64:\n    return p.a\n\ndef other(y: i64) -> i64:\n    return y\n"
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	// c: u8 before a: i64 costs 7 bytes a reordering would save.
	padded := strings.Replace(before, "struct P:\n", "struct P:\n    c: u8\n", 1)
	for i, src := range []string{padded, before} {
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("edit %d fell back: %v", i, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh)
		if got != want {
			t.Fatalf("edit %d diverged\n got:\n%s\nwant:\n%s", i, got, want)
		}
		if i == 0 && !strings.Contains(want, "padding") {
			t.Fatalf("fixture: the u8 field should cost padding:\n%s", want)
		}
	}
}

// Declaring an added function classifies region polymorphism for it alone; the program-wide
// tables that classification resets must keep every other function's entries, or later
// re-analysis of their callers loses the caller-arena binding.
func TestRetainedAddedFunctionKeepsRegionPolymorphismTables(t *testing.T) {
	options := AnalyzeOptions{}
	before := "def make_list(n: i64) -> darray[i64]:\n    out: mutable darray[i64] = []\n    out.push(n)\n    return out\n\n" +
		"def use_list() -> i64:\n    xs: darray[i64] = make_list(3)\n    return xs.count.i64()\n"
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	beforeArena := len(retained.a.regionPolyReturnsCallerArena)
	if beforeArena == 0 {
		t.Skip("fixture: no caller-arena function classified")
	}
	edited := before + "\ndef added(y: i64) -> i64:\n    return y\n"
	if _, err := retained.Update(retainedParse(t, edited), retainedParse(t, edited), []byte(edited)); err != nil {
		t.Fatalf("added function fell back: %v", err)
	}
	if got := len(retained.a.regionPolyReturnsCallerArena); got < beforeArena {
		t.Fatalf("caller-arena table shrank from %d to %d entries", beforeArena, got)
	}
	// A body edit to the caller after the add matches a fresh analysis.
	again := strings.Replace(edited, "return xs.count.i64()", "return xs.count.i64() + 1", 1)
	updated, err := retained.Update(retainedParse(t, again), retainedParse(t, again), []byte(again))
	if err != nil {
		t.Fatalf("caller edit fell back: %v", err)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, again), options)
	if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
		t.Fatalf("diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

const retainedOverloadBefore = `
def width(x: i64) -> i64:
    return x

def caller() -> i64:
    n: i64 = 2
    return width(true) + width(n)

def other(y: i64) -> i64:
    return y
`

// An overload added before or after the existing function is declared as a full analysis
// declares the pair (the first owns the bare name), and the caller that now resolves to it
// loses its error. Removing it again restores the error.
func TestRetainedAnalysisAcceptsAddedOverloads(t *testing.T) {
	options := AnalyzeOptions{}
	overload := "def width(x: bool) -> i64:\n    return 1\n\n"
	after := strings.Replace(retainedOverloadBefore, "def caller()", overload+"def caller()", 1)
	before := strings.Replace(retainedOverloadBefore, "\ndef width(x: i64)", "\n"+overload+"def width(x: i64)", 1)
	for name, edited := range map[string]string{"after": after, "before": before} {
		_, retained := AnalyzeRetained(retainedParse(t, retainedOverloadBefore), retainedParse(t, retainedOverloadBefore), []byte(retainedOverloadBefore), options)
		if len(AnalyzeWithOptions(retainedParse(t, retainedOverloadBefore), options).Errors()) == 0 {
			t.Fatal("fixture: the bool call should not resolve without the overload")
		}
		for i, src := range []string{edited, retainedOverloadBefore, edited} {
			updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
			if err != nil {
				t.Fatalf("%s step %d fell back: %v", name, i, err)
			}
			fresh := AnalyzeWithOptions(retainedParse(t, src), options)
			if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
				t.Fatalf("%s step %d diverged\n got:\n%s\nwant:\n%s", name, i, got, want)
			}
			if src != retainedOverloadBefore && len(fresh.Errors()) != 0 {
				t.Fatalf("fixture: the overload should resolve the bool call:\n%s", retainedDiagnostics(fresh))
			}
			// Each overload has the symbol name a fresh analysis gives it, by line.
			symbols := func(r *RetainedAnalysis) map[int]string {
				out := map[int]string{}
				for _, sd := range r.decls {
					if fn, ok := sd.Decl.(*ast.FuncDecl); ok && fn.Name == "width" {
						if sym := r.a.funcDeclSymbols[fn]; sym != nil {
							out[fn.Pos().Line] = sym.Name
						}
					}
				}
				return out
			}
			_, cold := AnalyzeRetained(retainedParse(t, src), retainedParse(t, src), []byte(src), options)
			got, want := symbols(retained), symbols(cold)
			if len(want) != strings.Count(src, "def width(") || fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("%s step %d: symbols %v, fresh %v", name, i, got, want)
			}
		}
	}
}

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

// A helper added inside an `extend` block's private section updates in place:
// it lands in the module's namespace with its section's visibility, so a use
// from outside the module is rejected exactly as a fresh analysis rejects it.
func TestRetainedAnalysisAcceptsAddedModuleMember(t *testing.T) {
	added := strings.Replace(retainedModuleBefore, "            return x + x\n",
		"            return x + x\n        def thrice(x: i64) -> i64:\n            v: mutable i64 = x\n            base: uintptr = (&v).cast[uintptr] can Unsafe.PointerCast\n            return x + x + x\n", 1)
	used := strings.Replace(added, "return twice(base(x))", "return thrice(twice(base(x)))", 1)
	leaked := strings.Replace(used, "return Tool::run(x)", "return Tool::thrice(x)", 1)
	if added == retainedModuleBefore || used == added || leaked == used {
		t.Fatal("fixture edit did not apply")
	}
	options := AnalyzeOptions{}
	before := AnalyzeWithOptions(retainedParse(t, retainedModuleBefore), options)
	if d := retainedDiagnostics(before); strings.Contains(d, "error") {
		t.Fatalf("fixture does not analyze cleanly:\n%s", d)
	}
	for _, step := range []string{added, used, leaked} {
		_, retained := AnalyzeRetained(retainedParse(t, retainedModuleBefore), retainedParse(t, retainedModuleBefore), []byte(retainedModuleBefore), options)
		if retained == nil {
			t.Fatal("analysis was not retained")
		}
		updated, err := retained.Update(retainedParse(t, step), retainedParse(t, step), []byte(step))
		if err != nil {
			t.Fatalf("adding a module member fell back: %v", err)
		}
		if retained.LastStats.Added != 1 {
			t.Fatalf("stats = %+v, want 1 added", retained.LastStats)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, step), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("retained update disagrees with a fresh analysis\nretained:\n%s\nfresh:\n%s", got, want)
		}
	}
	freshLeak := retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, leaked), options))
	if !strings.Contains(freshLeak, "thrice") {
		t.Fatalf("the private-use fixture is not rejected:\n%s", freshLeak)
	}
}

// A member's signature edit replaces it inside its block: callers are re-analyzed
// against the new signature, exactly as a fresh analysis sees them.
func TestRetainedAnalysisAcceptsModuleMemberSignatureEdits(t *testing.T) {
	steps := []string{
		strings.Replace(retainedModuleBefore, "def twice(x: i64) -> i64:\n            return x + x", "def twice(n: i64) -> i64:\n            return n + n", 1),
		strings.Replace(retainedModuleBefore, "def run(x: i64) -> i64:", "def run(x: i32) -> i64:", 1),
		strings.Replace(retainedModuleBefore, "def twice(x: i64) -> i64:", "def twice(x: bool) -> i64:", 1),
	}
	options := AnalyzeOptions{}
	for i, src := range steps {
		if src == retainedModuleBefore {
			t.Fatalf("fixture edit %d did not apply", i)
		}
		_, retained := AnalyzeRetained(retainedParse(t, retainedModuleBefore), retainedParse(t, retainedModuleBefore), []byte(retainedModuleBefore), options)
		if retained == nil {
			t.Fatal("analysis was not retained")
		}
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("member signature edit %d fell back: %v", i, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("member signature edit %d disagrees with a fresh analysis\nretained:\n%s\nfresh:\n%s", i, got, want)
		}
		if i == 2 && len(fresh.Errors()) == 0 {
			t.Fatal("fixture: the member's new parameter type must reject its caller")
		}
		// And back: the block keeps exactly one declaration of the member.
		reverted, err := retained.Update(retainedParse(t, retainedModuleBefore), retainedParse(t, retainedModuleBefore), []byte(retainedModuleBefore))
		if err != nil {
			t.Fatalf("revert of member edit %d fell back: %v", i, err)
		}
		if got, want := retainedDiagnostics(reverted), retainedDiagnostics(AnalyzeWithOptions(retainedParse(t, retainedModuleBefore), options)); got != want {
			t.Fatalf("revert of member edit %d diverged\n got:\n%s\nwant:\n%s", i, got, want)
		}
	}
}

const retainedStructBefore = `
struct Point:
    x: i64
    y: i64 = 0

def norm(p: Point) -> i64:
    return p.x + p.y

def make(v: i64) -> Point:
    return Point{x: v}

def unrelated(n: i64) -> i64:
    return n * 2
`

// An edit to a plain struct re-analyzes its users against the new fields and leaves the rest.
func TestRetainedAnalysisAcceptsPlainStructEdits(t *testing.T) {
	steps := []string{
		strings.Replace(retainedStructBefore, "    y: i64 = 0\n", "    y: i64 = 0\n    z: i64 = 1\n", 1),
		strings.Replace(retainedStructBefore, "    y: i64 = 0\n", "    y: bool = false\n", 1),
		strings.Replace(retainedStructBefore, "    x: i64\n", "", 1),
	}
	options := AnalyzeOptions{}
	for i, src := range steps {
		if src == retainedStructBefore {
			t.Fatalf("fixture edit %d did not apply", i)
		}
		_, retained := AnalyzeRetained(retainedParse(t, retainedStructBefore), retainedParse(t, retainedStructBefore), []byte(retainedStructBefore), options)
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("struct edit %d fell back: %v", i, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("struct edit %d disagrees with a fresh analysis\nretained:\n%s\nfresh:\n%s", i, got, want)
		}
		if i > 0 && len(fresh.Errors()) == 0 {
			t.Fatalf("fixture: struct edit %d must break a user", i)
		}
		// An added defaulted scalar field is invisible to the reader `norm`.
		want := 2
		if i == 0 {
			want = 1
		}
		if users := retained.LastStats.StructUsers; users != want {
			t.Fatalf("struct edit %d re-analyzed %d functions, want %d", i, users, want)
		}
	}
}

// A kept diagnostic names body-local gensyms after the byte offset of their expression; a
// body that moved takes the names a fresh analysis gives it, and only those names.
func TestRetainedShiftLocalGensyms(t *testing.T) {
	in := "known: __quantifier_100 <- false; __auto_7 and __MachineMode_ab_100 stay"
	want := "known: __quantifier_112 <- false; __auto_19 and __MachineMode_ab_100 stay"
	if got := retainedShiftLocalGensyms(in, 12); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := retainedShiftLocalGensyms(in, 0); got != in {
		t.Fatalf("a zero shift changed %q to %q", in, got)
	}
}

// A callee's contract is part of its signature: a caller's proof reads it, so editing it
// re-checks the caller.
func TestRetainedContractEditRechecksCallers(t *testing.T) {
	before := `
def pick(x: u8, y: u8) -> u8:
    ensure result == x
    r: mutable u8 = x
    for i in 0 ..< 3:
        r = x
    return r

def caller(x: u8, y: u8) -> u8:
    ensure result == x
    return pick(x, y)
`
	after := strings.Replace(before, "    ensure result == x\n    r:", "    ensure result == x or result == y\n    r:", 1)
	options := AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	for i, src := range []string{after, before, after} {
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if src == after && len(fresh.Errors()) == 0 {
			t.Fatalf("fixture: the weaker callee contract should leave caller's ensure unproven:\n%s", retainedDiagnostics(fresh))
		}
		updated, err := retained.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			continue // a fallback is a fresh analysis
		}
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d diverged\n got:\n%s\nwant:\n%s", i, got, want)
		}
	}
}

func TestRetainedRemovesModuleMemberAndRechecksCallers(t *testing.T) {
	before := `module Lib:
    def helper() -> i64:
        return 42

    def kept() -> i64:
        return 1

def main() -> i64:
    return Lib::helper()
`
	after := strings.Replace(before, "    def helper() -> i64:\n        return 42\n\n", "", 1)
	_, r := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	for _, src := range []string{after, before} {
		got, err := r.Update(retainedParse(t, src), retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("module member removal/revert fell back: %v", err)
		}
		want := AnalyzeWithOptions(retainedParse(t, src), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("module member update differs:\n%s\nwant:\n%s", retainedDiagnostics(got), retainedDiagnostics(want))
		}
	}
}

// Removing a function that returns caller-arena storage changes one declaration
// table entry. Surviving region summaries must still agree with cold analysis,
// including after a disk round trip and a later caller edit.
func TestRetainedRemovesCallerArenaFunctionAcrossDiskState(t *testing.T) {
	const removed = "def unused_list(n: i64) -> darray[i64]:\n    out: mutable darray[i64] = []\n    out.push(n)\n    return out\n\n"
	const survivor = "def make_list(n: i64) -> darray[i64]:\n    out: mutable darray[i64] = []\n    out.push(n)\n    return out\n\ndef use_list() -> i64:\n    xs: darray[i64] = make_list(3)\n    return xs.count.i64()\n"
	before := removed + survivor
	options := AnalyzeOptions{}
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), options)
	summaries := func(state *RetainedAnalysis) map[string]bool {
		out := map[string]bool{}
		for fn, sym := range state.a.funcDeclSymbols {
			if ft, ok := sym.Type.(*FuncType); ok {
				out[fn.Name] = state.a.regionPolyReturnsCallerArena[ft]
			}
		}
		return out
	}
	if !summaries(state)["unused_list"] || !summaries(state)["make_list"] {
		t.Fatal("fixture did not classify caller-arena returns")
	}
	for step, source := range []string{survivor, before, survivor, strings.Replace(survivor, "return xs.count.i64()", "return xs.count.i64() + 1", 1)} {
		encoded, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(encoded)
		if err != nil {
			t.Fatal(err)
		}
		updated, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatalf("step %d fell back: %v", step, err)
		}
		fresh, cold := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d diagnostics diverged\ngot %s\nwant %s", step, got, want)
		}
		got, want := summaries(state), summaries(cold)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("step %d region summaries diverged: %v versus %v", step, got, want)
		}
		for ft := range state.a.regionPolyReturnsCallerArena {
			if ft.Name == "unused_list" && !strings.Contains(source, "def unused_list") {
				t.Fatalf("step %d retains removed region summary", step)
			}
		}
	}
}

func TestRetainedRemovalDoesNotHideSurvivingRegionSummaryChange(t *testing.T) {
	const removed = "def unused_scalar(x: i64) -> i64:\n    return x\n\n"
	const maker = "def make_list() -> darray[i64]:\n    out: mutable darray[i64] = []\n    out.push(1)\n    return out\n"
	before := "global backing: darray[i64] = []\n\n" + removed + maker
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	after := strings.Replace(before, removed, "", 1)
	after = strings.Replace(after, maker, "def make_list() -> darray[i64]:\n    return backing\n", 1)
	_, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err == nil || !strings.Contains(err.Error(), "declaration results of ::make_list#0 changed") {
		t.Fatalf("expected surviving declaration mismatch, got %v", err)
	}
}

func TestRetainedOverloadEditsPreserveOtherNamespaces(t *testing.T) {
	const prefix = "# namespace probe\n"
	const top = "def pick(x: i64) -> i64:\n    return x + 1\n\n"
	const member = "        def pick(x: i64) -> i64:\n            return x + 2\n\n"
	const module = "module Outer:\n    module Lib:\n" + member + "        def kept() -> i64:\n            return 1\n\ndef caller() -> i64:\n    return Outer::Lib::pick(40)\n"
	const extra = "def pick(x: bool) -> i64:\n    return 3\n\n"
	before := prefix + top + module
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	cases := []struct{ source, keep string }{
		{prefix + module, "Outer.Lib.pick"}, {before, "Outer.Lib.pick"},
		{prefix + top + extra + module, "Outer.Lib.pick"}, {before, "Outer.Lib.pick"},
		{prefix + top + strings.Replace(module, member, "", 1), "pick"}, {before, "pick"},
		{prefix + module, "Outer.Lib.pick"},
	}
	for step, tc := range cases {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		symbol, ok := state.a.globalScope.Lookup(tc.keep)
		if !ok {
			t.Fatalf("fixture lost %s", tc.keep)
		}
		prior := symbol.Type
		updated, err := state.Update(retainedParse(t, tc.source), retainedParse(t, tc.source), []byte(tc.source))
		if err != nil {
			t.Fatalf("step %d fell back: %v", step, err)
		}
		symbol, ok = state.a.globalScope.Lookup(tc.keep)
		if !ok || symbol.Type != prior {
			t.Fatalf("step %d re-declared unrelated %s", step, tc.keep)
		}
		if state.a.functionTypes[tc.keep] != prior {
			t.Fatalf("step %d removed surviving function type %s", step, tc.keep)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, tc.source), AnalyzeOptions{})
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d diverged\ngot %s\nwant %s", step, got, want)
		}
	}
}

func TestRetainedRemovalRechecksSameNamedNamespaceCaller(t *testing.T) {
	const removed = "    def shared() -> i64:\n        return 1\n\n"
	before := "# namespace caller probe\nmodule Gone:\n" + removed + "    def kept() -> i64:\n        return 0\n\nmodule Kept:\n    def shared() -> i64:\n        return Gone::shared()\n"
	after := strings.Replace(before, removed, "", 1)
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	for step, source := range []string{after, before, after} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		updated, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if source == after && len(fresh.Errors()) == 0 {
			t.Fatal("fixture did not lose removed callee")
		}
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d diverged\ngot %s\nwant %s", step, got, want)
		}
	}
}
