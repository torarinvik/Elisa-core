//go:build cgo

package semantic

import (
	"fmt"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

const proofGeneratedBindingSource = `law Positive(self: i64) = self > 0

def first() -> i64:
    return 0

def moved(values: darray[i64]) -> i64 is Positive:
    if forall index in 0..<values.count: values[index] > 0:
        return 1
    return 2
`

func proofBindingFunction(t testing.TB, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range retainedWalkDecls(file) {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == name {
			return fn
		}
	}
	t.Fatal("missing function", name)
	return nil
}

// These are actual analyzer-produced report payloads containing parser-lowered
// runtime quantifier binders, rather than an injected proof certificate.
func TestLiveProofReportQuantifierPayloadRelocatesAcrossDisk(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	options := AnalyzeOptions{EnableSMT: true}
	result, state := AnalyzeRetained(retainedParse(t, proofGeneratedBindingSource), retainedParse(t, proofGeneratedBindingSource), []byte(proofGeneratedBindingSource), options)
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	if !strings.Contains(fmt.Sprint(result.ProofReport), "__quantifier_") {
		t.Fatal("real fixture did not expose a generated proof payload", result.ProofReport)
	}
	next := strings.Replace(proofGeneratedBindingSource, "    return 0", "    if true:\n        return 0\n    return 0", 1)
	for _, source := range []string{next, proofGeneratedBindingSource, next, proofGeneratedBindingSource} {
		disk, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(disk)
		if err != nil {
			t.Fatal(err)
		}
		got, err := state.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, source), options)
		assertExactLiveProofReport(t, cold, got)
	}
}

// This tests only the rendering adapter with real independently parsed binder
// nodes. It never inserts a record/certificate into an analyzer or proof map.
func TestLiveProofReportAutoBinderRenderingIsScopedAndQuoteSafe(t *testing.T) {
	const autoSource = `def first() -> i64:
    return 0

def moved() -> i64:
    in auto:
        return 0
`
	initial, state := AnalyzeRetained(retainedParse(t, autoSource), retainedParse(t, autoSource), []byte(autoSource), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	previous := proofBindingFunction(t, state.pristine, "moved")
	oldRegion, ok := previous.Body[0].(*ast.RegionStmt)
	if !ok || !oldRegion.Lazy {
		t.Fatal("missing parser auto binder")
	}
	// A blank source line is enough to change both generated binding epochs.
	source := "\n" + autoSource
	file := retainedParse(t, source)
	current := proofBindingFunction(t, file, "moved")
	newRegion, ok := current.Body[0].(*ast.RegionStmt)
	if !ok || !newRegion.Lazy {
		t.Fatal("missing fresh parser auto binder")
	}
	marks := map[string][][2]int{previous.Pos().File: {{1, 1}}}
	render := state.prepareProofReportBindings(file, marks)
	row := proofContribution{Key: proofContributionKey{Owner: current, Phase: proofPhaseBody}, Origins: map[ast.Node]bool{current: true}}
	text := oldRegion.Name + " == " + oldRegion.Name + " and literal == " + fmt.Sprintf("%q", oldRegion.Name)
	fact, why := render(row, ProofFact{Subject: text})
	if why != "" {
		t.Fatal(why)
	}
	expected := newRegion.Name + " == " + newRegion.Name + " and literal == " + fmt.Sprintf("%q", oldRegion.Name)
	if fact.Subject != expected {
		t.Fatalf("identifier binding/literal preservation: got %q want %q", fact.Subject, expected)
	}
	if _, why := render(row, ProofFact{Subject: "__unknown_123"}); why == "" {
		t.Fatal("unsupported offset naming family silently retained")
	}
	for _, text := range []string{fmt.Sprintf("%q", "__unknown_123"), "π" + oldRegion.Name} {
		unchanged, why := render(row, ProofFact{Subject: text})
		if why != "" || unchanged.Subject != text {
			t.Fatalf("literal/Unicode identifier changed: %q %v", unchanged.Subject, why)
		}
	}
	sibling := proofBindingFunction(t, file, "first")
	row.Key.Owner = sibling
	row.Origins = map[ast.Node]bool{sibling: true}
	if _, why := render(row, ProofFact{Subject: oldRegion.Name}); why == "" {
		t.Fatal("sibling producer authorized foreign token spelling")
	}
	for _, extra := range []string{
		"\ndef lookalike(" + oldRegion.Name + ": i64) -> i64:\n    return 0\n",
		"\ndef nested() -> i64:\n    if true:\n        mutable " + oldRegion.Name + ": i64 = 0\n    return 0\n",
	} {
		forged := retainedParse(t, source+extra)
		reject := state.prepareProofReportBindings(forged, marks)
		row.Key.Owner = current
		row.Origins = map[ast.Node]bool{current: true}
		if _, why := reject(row, ProofFact{Subject: oldRegion.Name}); why == "" {
			t.Fatal("user/nested lookalike spelling authorized as generated reference")
		}
	}
}

// A producer-composed span need not appear as a whole on any source node.
// Each endpoint still has exact source syntax authority. This pure renderer
// test does not create or mutate semantic proof records.
func TestLiveProofReportSyntheticSpanUsesIndependentSourceEndpoints(t *testing.T) {
	previous := retainedParse(t, proofGeneratedBindingSource)
	source := strings.Replace(proofGeneratedBindingSource, "    return 0", "    if true:\n        return 0\n    return 0", 1)
	current := retainedParse(t, source)
	oldStart := proofBindingFunction(t, previous, "first").Pos()
	oldEnd := proofBindingFunction(t, previous, "moved").Pos()
	newStart := proofBindingFunction(t, current, "first").Pos()
	newEnd := proofBindingFunction(t, current, "moved").Pos()
	combined := oldStart
	combined.EndLine, combined.EndCol, combined.EndOffset = oldEnd.Line, oldEnd.Col, oldEnd.Offset
	marks := map[string][][2]int{oldEnd.File: {{oldEnd.Line, 2}}}
	render := proofReportSyntaxRenderer(current, marks)
	got, why := render(combined, true)
	if why != "" {
		t.Fatal(why)
	}
	expected := newStart
	expected.EndLine, expected.EndCol, expected.EndOffset = newEnd.Line, newEnd.Col, newEnd.Offset
	if got != expected {
		t.Fatalf("synthetic span differs: got %#v want %#v", got, expected)
	}
	startOnly := lexer.Pos{File: oldEnd.File, Line: oldEnd.Line, Col: oldEnd.Col, Offset: oldEnd.Offset}
	got, why = render(startOnly, true)
	if why != "" {
		t.Fatal(why)
	}
	if got.Offset != newEnd.Offset || got.Line != newEnd.Line || got.EndLine != 0 || got.EndCol != 0 || got.EndOffset != 0 {
		t.Fatalf("start-only producer acquired an end: %#v", got)
	}
}
