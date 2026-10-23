package semantic

import (
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
	added := steps[1] + "\nconst EXTRA: i64 = 1\n"
	if _, err := retained.Update(retainedParse(t, added), retainedParse(t, added), []byte(added)); err == nil {
		t.Fatal("adding a non-function declaration must fall back")
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
		if users := retained.LastStats.StructUsers; users != 2 {
			t.Fatalf("struct edit %d re-analyzed %d functions, want the two users", i, users)
		}
	}
}
