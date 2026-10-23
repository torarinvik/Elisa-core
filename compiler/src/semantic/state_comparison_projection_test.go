package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestSemanticSnapshotProjectsOnlyPristineBodies(t *testing.T) {
	pristine := retainedParse(t, retainedBefore)
	_, state := AnalyzeRetained(retainedParse(t, retainedBefore), pristine, []byte(retainedBefore), AnalyzeOptions{})
	originals := map[*ast.FuncDecl]int{}
	for _, decl := range retainedWalkDecls(pristine) {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			originals[fn] = len(fn.Body)
		}
	}
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	for fn, n := range originals {
		if len(fn.Body) != n {
			t.Fatal("encoding modified caller-owned pristine body")
		}
	}
	restored, err := RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, decl := range retainedWalkDecls(restored.pristine) {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			count++
			if len(fn.Body) != 0 {
				t.Fatal("snapshot retained unused pristine body")
			}
			if text, ok := restored.texts[fn]; !ok || text.header == "" {
				t.Fatal("snapshot lost pristine comparison text")
			}
		}
	}
	if count == 0 {
		t.Fatal("fixture has no pristine functions")
	}
	for _, sd := range restored.decls {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok && len(fn.Body) == 0 {
			t.Fatal("snapshot removed a live body")
		}
	}
	added := retainedAfter + "\ndef added(x: i64) -> i64:\n    return helper(x)\n"
	for _, source := range []string{retainedBefore, retainedAfter, added, "# shifted\n" + added, retainedBefore} {
		got, err := restored.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		want := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatalf("projected replay differs for %q", source)
		}
		data, err = restored.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		restored, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Headers and defaults must still invalidate and recheck callers.
	for _, source := range []string{
		strings.Replace(retainedBefore, "helper(x: i64)", "helper(x: bool)", 1),
		strings.Replace(retainedBefore, "helper(x: i64)", "helper(x: i64, y: i64 = 0)", 1),
	} {
		got, err := restored.Update(retainedParse(t, source), retainedParse(t, source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		want := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
		if retainedDiagnostics(got) != retainedDiagnostics(want) {
			t.Fatal("projected header edit differs from cold analysis")
		}
	}
}

func TestSemanticSnapshotKeepsSharedLiveBodies(t *testing.T) {
	_, state := AnalyzeRetained(retainedParse(t, retainedBefore), retainedParse(t, retainedBefore), []byte(retainedBefore), AnalyzeOptions{})
	// Conservatively preserve bodies even if an AST root aliases live declarations.
	state.pristine = state.a.file
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range retainedWalkDecls(restored.pristine) {
		if fn, ok := decl.(*ast.FuncDecl); ok && len(fn.Body) == 0 {
			t.Fatal("projection removed a shared live body")
		}
	}
}
