//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestExprFactsRestoreRejectsIndependentlyEncodedConstructionOverlay(t *testing.T) {
	const source = `def foreign(values: i32[4]) -> i32[4]:
    return values
`
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	if len(state.a.exprFacts) == 0 {
		t.Fatal("fixture has no actual canonical array facts")
	}
	// Bypass the public MarshalState guard deliberately: Restore must enforce its
	// own resource boundary even when a complete graph was independently encoded.
	base := state.a.exprFacts
	state.a.exprFacts = map[ast.Expr]OptimizationFacts{}
	state.a.exprFactsOverlay = &analyzerExprFactsOverlay{base: base}
	encoded, err := semanticStateRegistry().Encode(state)
	if err != nil {
		t.Fatal("independent active-resource graph could not be encoded", err)
	}
	restored, err := RestoreAnalysisState(encoded)
	if restored != nil || err == nil || !strings.Contains(err.Error(), "construction expression-facts overlay") {
		t.Fatalf("Restore admitted active partial facts or wrong rejection: result=%v error=%v", restored != nil, err)
	}
}
