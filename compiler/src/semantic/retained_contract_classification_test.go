//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestRetainedUnchangedNamedContractAllowsBodyEditAcrossDisk(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	oldMode := retainedFastPrepareMode
	retainedFastPrepareMode = ""
	t.Cleanup(func() { retainedFastPrepareMode = oldMode })
	const source = `contract Positive(x: i64):
    requires x > 0

contract Bounded(x: i64):
    requires x < 100

def consume(value: i64) -> i64:
    uses Positive(value)
    uses Bounded(value)
    return value
`
	initial, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	uses := 0
	for _, fact := range initial.ProofReport {
		if fact.Predicate == "uses" {
			uses++
		}
	}
	if uses != 2 {
		t.Fatalf("actual contract applications=%d want2", uses)
	}
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	after := strings.Replace(source, "return value", "return value + 0", 1)
	warm, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	if retainedDiagnostics(cold) != retainedDiagnostics(warm) {
		t.Fatal("body edit changed diagnostics")
	}
	assertRetainedCanonicalLiveResultParity(t, cold, warm)
}

func TestRetainedContractMarkerDoesNotClassifyUnknownCallable(t *testing.T) {
	a := &Analyzer{}
	if got := a.preBodyFunctionClassification(&ast.FuncDecl{Name: "Spec", IsContract: true}); got != "non-callable named contract" {
		t.Fatalf("contract marker=%q", got)
	}
	if got := a.preBodyFunctionClassification(&ast.FuncDecl{Name: "callable"}); got != "unavailable" {
		t.Fatalf("missing callable authority was accepted: %q", got)
	}
}
