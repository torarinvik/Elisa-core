package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"fmt"
	"sort"
	"strings"
	"testing"
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
