//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestRetainedRemovedPublicFunctionRetractsVisibilityAcrossDisk(t *testing.T) {
	const before = `module Sample:
    public:
        def kept() -> i32:
            return 0

        def extra() -> i32:
            return 7

def main() -> i32:
    return Sample::kept()
`
	after := strings.Replace(before, "\n        def extra() -> i32:\n            return 7\n", "", 1)
	_, state := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if state == nil {
		t.Fatal("baseline was not retained")
	}
	checkpoint, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	var removed *ast.FuncDecl
	for _, decl := range state.decls {
		if fn, ok := decl.Decl.(*ast.FuncDecl); ok && fn.Name == "extra" {
			removed = fn
		}
	}
	if removed == nil || state.a.file.DeclVisibility[removed] != "public" {
		t.Fatal("fixture did not retain the public member's visibility")
	}
	updated, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	if _, present := state.a.declVisibility[removed]; present {
		t.Fatal("removed function remains in analyzer visibility")
	}
	if _, present := state.a.file.DeclVisibility[removed]; present {
		t.Fatal("removed function remains in file visibility")
	}
	cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	assertRetainedCanonicalLiveResultParity(t, cold, updated)
	checkpoint, err = state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreAnalysisState(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	result, matches := restored.ResultForSource([]byte(after))
	if !matches || result == nil {
		t.Fatal("updated source did not match the restored state")
	}
	assertRetainedCanonicalLiveResultParity(t, cold, result)
}
