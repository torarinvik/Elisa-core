//go:build cgo

package backend

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// Real lowering settles lazy canonical queries before the next declaration
// transaction. Newly registered owners must enter the actual source graph before
// canonical product validation; declaration-table membership alone is insufficient.
func TestPlainDefaultLifecycleAfterRealCodegenCatalogAndDiskRoundtrip(t *testing.T) {
	const prefix = "global values: i32[2] = [11, 22]\nglobal cursor: usize = 0\ndef keep() -> i32:\n    return 1\n"
	const first = "def first(value: i32 = values[cursor]) -> i32:\n    return value\n"
	parse := func(source string) *ast.File {
		return parseRetainedDisjointCacheSource(t, "plain-default-catalog-roundtrip.elisa", source)
	}
	initial, state := semantic.AnalyzeRetained(parse(prefix+first), parse(prefix+first), []byte(prefix+first), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	_ = retainedDisjointIR(t, initial)
	for _, source := range []string{prefix, prefix + first, prefix, prefix + first} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = semantic.RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		warm, err := state.Update(parse(source), parse(source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		cold := semantic.AnalyzeWithOptions(parse(source), semantic.AnalyzeOptions{})
		if !reflect.DeepEqual(warm.ProofReport, cold.ProofReport) || warm.ElisionSummary != cold.ElisionSummary || !reflect.DeepEqual(warm.Diagnostics, cold.Diagnostics) {
			t.Fatal("raw report/diagnostic metadata differs")
		}
		if got, want := retainedDisjointIR(t, warm), retainedDisjointIR(t, cold); got != want {
			t.Fatalf("actual catalog/disk update IR differs without normalization\nwarm:\n%s\ncold:\n%s", got, want)
		}
	}
}
