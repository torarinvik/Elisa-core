//go:build cgo

package semantic

import (
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
)

const retainedLineShiftScalar = `def calculate(value: i64) -> i64:
    return value + 1

struct P:
    c: u8
    a: i64
    b: u8

def other(value: i64) -> i64:
    return value + 2
`

func retainedLineShiftLazyUpdate(t *testing.T, state *RetainedAnalysis, source string, wantParses int) *Result {
	t.Helper()
	calls := 0
	scratch := NewLazyParsedRetainedScratch(func() (*ast.File, error) {
		calls++
		return retainedParse(t, source), nil
	}, AnalyzeOptions{})
	warm, err := state.UpdatePrepared(retainedParse(t, source), nil, []byte(source), scratch)
	if err != nil {
		t.Fatal(err)
	}
	if wantParses != 0 {
		scratch.Wait()
	}
	if calls != wantParses || state.LastStats.FastPrepared != (wantParses == 0) || state.LastStats.ScratchPrepared != (wantParses == 1) {
		t.Fatalf("parse calls=%d want=%d stats=%+v", calls, wantParses, state.LastStats)
	}
	if wantParses == 0 && (scratch.file != nil || scratch.pre != nil) {
		t.Fatal("unused lazy scratch materialized")
	}
	cold := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
	if len(cold.Errors()) != 0 || len(warm.Errors()) != 0 {
		t.Fatalf("cold=%v warm=%v", cold.Errors(), warm.Errors())
	}
	if retainedDiagnostics(cold) != retainedDiagnostics(warm) {
		t.Fatalf("diagnostics differ\ncold=%s\nwarm=%s", retainedDiagnostics(cold), retainedDiagnostics(warm))
	}
	assertRetainedCanonicalLiveResultParity(t, cold, warm)
	// Canonical semantic parity deliberately ignores lexer.Pos. Check printed
	// declaration source coordinates independently, preserving stable offsets.
	for i, decl := range cold.File.Decls {
		got, want := warm.File.Decls[i].Pos(), decl.Pos()
		if got.File != want.File || got.Line != want.Line || got.Col != want.Col || got.EndLine != want.EndLine || got.EndCol != want.EndCol {
			t.Fatalf("declaration %d source coordinates differ: warm=%v cold=%v", i, got, want)
		}
	}
	return warm
}

func TestRetainedLineShiftFastBodyRelocatesWarningAndInvalidatesNextCertificate(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	initial, state := AnalyzeRetained(retainedParse(t, retainedLineShiftScalar), retainedParse(t, retainedLineShiftScalar), []byte(retainedLineShiftScalar), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	state = reloadRetainedFastPrepareForTest(t, state)
	oldPre := state.pre
	oldDiags := append([]Diagnostic(nil), oldPre.diags...)
	if len(oldDiags) == 0 || !strings.Contains(retainedDiagnostics(initial), "padding") {
		t.Fatal("fixture has no real declaration padding warning")
	}
	after := strings.Replace(retainedLineShiftScalar, "    return value + 1", "    if value < 3:\n        return 0\n    return value + 1", 1)
	retainedLineShiftLazyUpdate(t, state, after, 0)
	if !reflect.DeepEqual(oldPre.diags, oldDiags) {
		t.Fatal("fast admission mutated previous declaration diagnostics")
	}
	if state.fastPrepareInvalidated == "" || state.classify != nil || state.digestOf != nil {
		t.Fatal("line movement requalified stale fast authority")
	}
	state = reloadRetainedFastPrepareForTest(t, state)
	second := strings.Replace(after, "value + 1", "value + 3", 1)
	retainedLineShiftLazyUpdate(t, state, second, 1)
	state = reloadRetainedFastPrepareForTest(t, state)
	retainedLineShiftLazyUpdate(t, state, retainedLineShiftScalar, 1)
}

func TestRetainedLineShiftFastBodyRetractsProofAndMovesUnaffectedProof(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	const source = `def read(xs: darray[i32]&, i: usize) -> i32:
    trusted Unsafe.UncheckedIndex:
        if i < xs.count:
            return xs[i]
        return 0

def other(xs: darray[i32]&, i: usize) -> i32:
    trusted Unsafe.UncheckedIndex:
        if i < xs.count:
            return xs[i]
        return 0
`
	_, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	state = reloadRetainedFastPrepareForTest(t, state)
	old := map[string]*ast.IndexExpr{}
	for _, decl := range state.result.File.Decls {
		fn := decl.(*ast.FuncDecl)
		index := fn.Body[0].(*ast.CanStmt).Body[0].(*ast.IfStmt).Then[0].(*ast.ReturnStmt).Value.(*ast.IndexExpr)
		if !state.result.IndexBoundsProven[index] {
			t.Fatalf("actual %s access not proved", fn.Name)
		}
		old[fn.Name] = index
	}
	oldOtherLine := old["other"].Pos().Line
	after := strings.Replace(source, "    trusted Unsafe.UncheckedIndex:", "    if i == 0:\n        return 0\n    trusted Unsafe.UncheckedIndex:", 1)
	after = strings.Replace(after, "if i < xs.count:", "if i > xs.count:", 1)
	warm := retainedLineShiftLazyUpdate(t, state, after, 0)
	if _, present := warm.IndexBoundsProven[old["read"]]; present {
		t.Fatal("detached edited proof survived")
	}
	if !warm.IndexBoundsProven[old["other"]] || old["other"].Pos().Line != oldOtherLine+2 {
		t.Fatal("unaffected proof was dropped or not relocated exactly once")
	}
}

func TestRetainedLineShiftFastDeclineMaterializesIndependentScratchOnce(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	state := lazyParseRetainedBaseline(t)
	after := strings.Replace(lazyParseRetainedSource, "return first(4)", "if true:\n        return second(4)\n    return 0", 1)
	// A changed callee set must retain the ordinary complete certificate decline.
	retainedLineShiftLazyUpdate(t, state, after, 1)
}

func TestRetainedLineShiftFastMovesEditedSecondFunction(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	source := retainedLineShiftScalar
	initial, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	state = reloadRetainedFastPrepareForTest(t, state)
	after := strings.Replace(source, "    return value + 1", "    if value < 3:\n        return 0\n    return value + 1", 1)
	after = strings.Replace(after, "return value + 2", "return value + 4", 1)
	retainedLineShiftLazyUpdate(t, state, after, 0)
	if state.LastStats.Changed != 2 {
		t.Fatalf("changed=%d want=2", state.LastStats.Changed)
	}
}

func TestRetainedLineShiftFastKeepsIndexedDefaultOwnerAndPositions(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	prefix := "def calculate(value: i64) -> i64:\n    return value + 1\n\n"
	// This actual source fixture already requires a proved indexed default.
	source := prefix + retainedProofNamedDefaultSource
	initial, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	state = reloadRetainedFastPrepareForTest(t, state)
	owner := retainedProofNamedDecl(t, state.result, "consume").(*ast.FuncDecl)
	index := owner.Params[0].DefaultValue.(*ast.IndexExpr)
	if !state.result.IndexBoundsProven[index] || state.a.indexBoundsProofOwners[index] != owner || !state.a.indexBoundsProofHeaders[index] {
		t.Fatal("actual header proof missing its exact role/owner")
	}
	oldLine := index.Pos().Line
	after := strings.Replace(source, "    return value + 1", "    if value < 3:\n        return 0\n    return value + 1", 1)
	warm := retainedLineShiftLazyUpdate(t, state, after, 0)
	if !warm.IndexBoundsProven[index] || state.a.indexBoundsProofOwners[index] != owner || !state.a.indexBoundsProofHeaders[index] || index.Pos().Line != oldLine+2 {
		t.Fatal("unchanged default proof authority or positions changed incorrectly")
	}
}

func TestRetainedLineShiftFastDeclinesMovedHookAuthority(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	prefix := "def calculate(value: i64) -> i64:\n    return value + 1\n\n"
	cases := []struct{ name, body, hook string }{
		{"cast", "struct Span:\n    value: i64\n\ndef __cast__(value: Span) -> i64:\n    return value.value\n", "__cast__"},
		{"constructor", "struct Span:\n    value: i64\n\ndef Span(value: i64) -> Span:\n    return Span{value}\n", "Span"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := prefix + c.body
			initial, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
			if len(initial.Errors()) != 0 {
				t.Fatal(initial.Errors())
			}
			oldName := ""
			for fn, symbol := range state.a.funcDeclSymbols {
				if fn.Name == c.hook {
					oldName = symbol.Name
				}
			}
			state = reloadRetainedFastPrepareForTest(t, state)
			after := strings.Replace(source, "    return value + 1", "    if value < 3:\n        return 0\n    return value + 1", 1)
			cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
			if len(cold.Errors()) != 0 {
				t.Fatal(cold.Errors())
			}
			freshName := ""
			for fn, symbol := range cold.analyzer.funcDeclSymbols {
				if fn.Name == c.hook {
					freshName = symbol.Name
				}
			}
			if oldName == "" || freshName == oldName {
				t.Fatal("fixture did not change actual declaration-produced symbol identity")
			}
			calls := 0
			scratch := NewLazyParsedRetainedScratch(func() (*ast.File, error) { calls++; return retainedParse(t, after), nil }, AnalyzeOptions{})
			warm, err := state.UpdatePrepared(retainedParse(t, after), nil, []byte(after), scratch)
			if state.LastStats.FastPrepared || calls != 1 || !strings.Contains(state.LastStats.FastDeclined, "location-derived hook symbol") {
				t.Fatalf("stale symbol authority bypassed scratch: %+v parses=%d err=%v", state.LastStats, calls, err)
			}
			if err == nil {
				assertRetainedCanonicalLiveResultParity(t, cold, warm)
			}
		})
	}
}

func TestRetainedLineShiftFastDeclinesDeclarationErrors(t *testing.T) {
	enableRetainedFastPrepareForTest(t)
	const source = "def calculate(value: i64) -> i64:\n    return value + 1\n\nglobal invalid: Missing = 0\n"
	_, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	hasError := false
	for _, d := range state.pre.diags {
		if d.Severity == "" || d.Severity == DiagnosticSeverityError {
			hasError = true
		}
	}
	if !hasError {
		t.Fatal("fixture has no real declaration-prefix error")
	}
	after := strings.Replace(source, "    return value + 1", "    if value < 3:\n        return 0\n    return value + 1", 1)
	calls := 0
	scratch := NewLazyParsedRetainedScratch(func() (*ast.File, error) { calls++; return retainedParse(t, after), nil }, AnalyzeOptions{})
	warm, err := state.UpdatePrepared(retainedParse(t, after), nil, []byte(after), scratch)
	if state.LastStats.FastPrepared || calls != 1 || !strings.Contains(state.LastStats.FastDeclined, "declaration-prefix errors") {
		t.Fatalf("invalid prefix fast-admitted: %+v err=%v", state.LastStats, err)
	}
	if err == nil {
		cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
		if retainedDiagnostics(cold) != retainedDiagnostics(warm) {
			t.Fatal("error diagnostics diverged")
		}
	}
}
