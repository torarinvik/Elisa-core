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
	updated, err := retained.Update(retainedParse(t, retainedAfter), []byte(retainedAfter))
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
	reverted, err := retained.Update(retainedParse(t, retainedBefore), []byte(retainedBefore))
	if err != nil {
		t.Fatalf("revert fell back: %v", err)
	}
	if got, want := retainedDiagnostics(reverted), retainedDiagnostics(before); got != want {
		t.Fatalf("revert diverged\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRetainedAnalysisRejectsSignatureEdits(t *testing.T) {
	_, retained := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), AnalyzeOptions{})
	edited := strings.Replace(retainedBefore, "def caller(x: i64) -> i64:", "def caller(x: i32) -> i64:", 1)
	if _, err := retained.Update(retainedParse(t, edited), []byte(edited)); err == nil || !strings.Contains(err.Error(), "signature of caller changed") {
		t.Fatalf("signature edit: err = %v", err)
	}
	added := retainedBefore + "\nconst EXTRA: i64 = 1\n"
	if _, err := retained.Update(retainedParse(t, added), []byte(added)); err == nil {
		t.Fatal("adding a declaration must fall back")
	}
}

func TestRetainedAnalysisReanalyzesFunctionsWhoseLinesMoved(t *testing.T) {
	options := AnalyzeOptions{}
	_, retained := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), options)
	// A comment inside the last function moves only its own statement.
	edited := strings.Replace(retainedBefore, "def later(y: i64) -> i64:\n", "def later(y: i64) -> i64:\n    # moved\n", 1)
	updated, err := retained.Update(retainedParse(t, edited), []byte(edited))
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
		updated, err := retained.Update(retainedParse(t, src), []byte(src))
		if err != nil {
			t.Fatalf("step %d fell back: %v", i, err)
		}
		fresh := AnalyzeWithOptions(retainedParse(t, src), options)
		if got, want := retainedDiagnostics(updated), retainedDiagnostics(fresh); got != want {
			t.Fatalf("step %d diverged\n got:\n%s\nwant:\n%s", i, got, want)
		}
	}
}
