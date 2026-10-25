//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func assertPlainDefaultRawColdParity(t *testing.T, warm, cold *Result, removed []ast.Node) {
	t.Helper()
	assertIncrementalResultParity(t, cold, warm)
	// Count raw public tables in addition to projected owner-aware parity.
	before, after := reflect.ValueOf(cold).Elem(), reflect.ValueOf(warm).Elem()
	for i := 0; i < before.NumField(); i++ {
		if !before.Type().Field(i).IsExported() || before.Field(i).Kind() != reflect.Map {
			continue
		}
		if before.Field(i).Type().Key().Implements(astNodeType) && before.Field(i).Len() != after.Field(i).Len() {
			t.Fatalf("raw %s cardinality differs: cold=%d warm=%d", before.Type().Field(i).Name, before.Field(i).Len(), after.Field(i).Len())
		}
	}
	for _, node := range removed {
		key := reflect.ValueOf(node)
		for _, path := range specializationASTMapPaths() {
			table := specializationField(reflect.ValueOf(warm.analyzer).Elem(), path)
			if !table.IsNil() && key.Type().AssignableTo(table.Type().Key()) && table.MapIndex(key).IsValid() {
				t.Fatalf("removed node remains in raw %s", reflect.TypeOf(*warm.analyzer).FieldByIndex(path).Name)
			}
		}
	}
	if !reflect.DeepEqual(cold.ProofReport, warm.ProofReport) || cold.ElisionSummary != warm.ElisionSummary {
		t.Fatal("cold report/elision state differs")
	}
}

func TestPlainDefaultLifecycleAcceptedSharedObserverRemovalAndRestore(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	before := plainDefaultProductPrefix + plainDefaultProductFirst + plainDefaultProductSecond
	after := plainDefaultProductPrefix + plainDefaultProductSecond
	initial, state := plainDefaultProductFixture(t, before)
	owner := retainedProofNamedDecl(t, initial, "first").(*ast.FuncDecl)
	removed := append([]ast.Node(nil), state.plainDefaults.Candidates[owner].Nodes...)
	if len(removed) == 0 || state.pre.plainGlobal == "" {
		t.Fatal("fresh producer omitted exact ownership/header residue")
	}
	blob, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(blob)
	if err != nil {
		t.Fatal(err)
	}
	// Use restored pointers in the raw removal oracle, not the old process graph.
	restoredOwner := retainedProofNamedDecl(t, state.result, "first").(*ast.FuncDecl)
	removed = append([]ast.Node(nil), state.plainDefaults.Candidates[restoredOwner].Nodes...)
	warm, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	assertPlainDefaultRawColdParity(t, warm, cold, removed)
	if state.LastStats.Removed != 1 {
		t.Fatal("did not take actual retained removal path")
	}
	blob, err = state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreAnalysisState(blob)
	if err != nil {
		t.Fatal(err)
	}
	assertPlainDefaultRawColdParity(t, restored.result, cold, nil)
}

func TestPlainDefaultLifecycleSharedDefaultReplacementAndRevert(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	prefix := plainDefaultProductPrefix + "global cursor2: usize = 1\n"
	third := `def third(value: i32 = values[cursor2]) -> i32:
    return value
`
	before := prefix + plainDefaultProductFirst + plainDefaultProductSecond + third
	replacement := `def first(value: i32 = values[cursor2]) -> i32:
    return value
`
	after := prefix + replacement + plainDefaultProductSecond + third
	initial, state := plainDefaultProductFixture(t, before)
	old := retainedProofNamedDecl(t, initial, "first").(*ast.FuncDecl)
	removed := append([]ast.Node(nil), state.plainDefaults.Candidates[old].Nodes...)
	warm, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	assertPlainDefaultRawColdParity(t, warm, cold, removed)
	warm, err = state.Update(retainedParse(t, before), retainedParse(t, before), []byte(before))
	if err != nil {
		t.Fatal(err)
	}
	cold = AnalyzeWithOptions(retainedParse(t, before), AnalyzeOptions{})
	assertPlainDefaultRawColdParity(t, warm, cold, nil)
}

func TestPlainDefaultLifecycleHistoricalBodyRemovalHasExactRawColdState(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	before := plainDefaultProductPrefix + plainDefaultProductFirst + plainDefaultProductSecond
	changed := plainDefaultProductPrefix + `def first(value: i32 = values[cursor]) -> i32:
    return 7
` + plainDefaultProductSecond
	initial, state := plainDefaultProductFixture(t, before)
	owner := retainedProofNamedDecl(t, initial, "first").(*ast.FuncDecl)
	body := newSpecializationSyntax()
	body.prepare(reflect.ValueOf(owner.Body))
	if err := body.validate(); err != nil {
		t.Fatal(err)
	}
	retired := append([]ast.Node(nil), body.prepared...)
	warm, err := state.Update(retainedParse(t, changed), retainedParse(t, changed), []byte(changed))
	if err != nil {
		t.Fatal(err)
	}
	assertPlainDefaultRawColdParity(t, warm, AnalyzeWithOptions(retainedParse(t, changed), AnalyzeOptions{}), retired)
	if state.plainDefaults.Candidates[owner].Invalidated {
		t.Fatal("certified retired body was not rebound")
	}
	blob, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(blob)
	if err != nil {
		t.Fatal(err)
	}
	owner = retainedProofNamedDecl(t, state.result, "first").(*ast.FuncDecl)
	removed := append([]ast.Node(nil), state.plainDefaults.Candidates[owner].Nodes...)
	after := plainDefaultProductPrefix + plainDefaultProductSecond
	warm, err = state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	assertPlainDefaultRawColdParity(t, warm, AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{}), removed)
}

func TestPlainDefaultLifecycleLastObserverRemovalAndRevert(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const keep = "def keep() -> i32:\n    return 1\n"
	before := plainDefaultProductPrefix + keep + plainDefaultProductFirst
	after := plainDefaultProductPrefix + keep
	initial, state := plainDefaultProductFixture(t, before)
	owner := retainedProofNamedDecl(t, initial, "first").(*ast.FuncDecl)
	removed := append([]ast.Node(nil), state.plainDefaults.Candidates[owner].Nodes...)
	if len(state.a.optimizationSymbolIDs) == 0 {
		t.Fatal("fixture has no actual global identity observations")
	}
	nextBefore := state.a.optimizationIdentityNext.Load()
	warm, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	if state.a.optimizationIdentityNext.Load() < nextBefore {
		t.Fatal("retiring mappings reused optimization identity counter")
	}
	cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	assertPlainDefaultRawColdParity(t, warm, cold, removed)
	if len(state.a.optimizationSymbolIDs) != len(cold.analyzer.optimizationSymbolIDs) {
		t.Fatal("last observer left a stale raw global identity registration")
	}
	warm, err = state.Update(retainedParse(t, before), retainedParse(t, before), []byte(before))
	if err != nil {
		t.Fatal(err)
	}
	assertPlainDefaultRawColdParity(t, warm, AnalyzeWithOptions(retainedParse(t, before), AnalyzeOptions{}), nil)
}

func TestPlainDefaultLifecycleForeignCallerRemainsUnsupported(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	before := plainDefaultProductPrefix + plainDefaultProductFirst + plainDefaultProductSecond + "def caller() -> i32:\n    return first()\n"
	after := plainDefaultProductPrefix + plainDefaultProductSecond + "def caller() -> i32:\n    return first()\n"
	initial, state := plainDefaultProductFixture(t, before)
	count := len(initial.ExprFacts)
	if _, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after)); err == nil {
		t.Fatal("foreign caller contribution was admitted without complete ownership")
	}
	if state.a == nil || len(initial.ExprFacts) != count {
		t.Fatal("unsupported caller transaction mutated graph")
	}
}

func TestPlainDefaultLifecycleActiveAndPoisonedSnapshotsDecline(t *testing.T) {
	_, state := plainDefaultProductFixture(t, plainDefaultProductPrefix+plainDefaultProductFirst)
	state.plainDefaults.Updating = true
	if _, err := state.MarshalState(); err == nil {
		t.Fatal("active contribution transaction persisted")
	}
	state.plainDefaults.Updating = false
	state.plainDefaults.Poisoned = true
	if _, err := state.MarshalState(); err == nil {
		t.Fatal("poisoned contribution graph persisted")
	}
}

func TestPlainDefaultLifecycleParallelBaselineRemovalMatchesCold(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "4")
	var extra strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&extra, "def plain_%d() -> i32:\n    return %d\n", i, i)
	}
	before := plainDefaultProductPrefix + plainDefaultProductFirst + plainDefaultProductSecond + extra.String()
	after := plainDefaultProductPrefix + plainDefaultProductSecond + extra.String()
	initial, state := plainDefaultProductFixture(t, before)
	owner := retainedProofNamedDecl(t, initial, "first").(*ast.FuncDecl)
	removed := append([]ast.Node(nil), state.plainDefaults.Candidates[owner].Nodes...)
	warm, err := state.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
	assertPlainDefaultRawColdParity(t, warm, cold, removed)
}

func TestPlainDefaultLifecycleReportAndOverlayCaptureRejects(t *testing.T) {
	_, state := plainDefaultProductFixture(t, plainDefaultProductPrefix+plainDefaultProductFirst)
	owner := retainedProofNamedDecl(t, state.result, "first").(*ast.FuncDecl)
	state.a.exprFactsOverlay = &analyzerExprFactsOverlay{}
	if _, err := captureRetainedPlainDefaultProduct(state.a, owner); err == nil {
		t.Fatal("construction fact overlay became canonical contribution authority")
	}
	state.a.exprFactsOverlay = nil
	state.a.exprTypeOverlay = &analyzerExprTypeOverlay{}
	if _, err := captureRetainedPlainDefaultProduct(state.a, owner); err == nil {
		t.Fatal("construction type overlay became canonical contribution authority")
	}
	state.a.exprTypeOverlay = nil
	state.a.iterBinderElementOverlay = &analyzerIterBinderElementOverlay{}
	if _, err := captureRetainedPlainDefaultProduct(state.a, owner); err == nil {
		t.Fatal("construction iterator overlay became canonical contribution authority")
	}
	state.a.iterBinderElementOverlay = nil
	state.a.currentProofCategory = "pending"
	if _, err := captureRetainedPlainDefaultProduct(state.a, owner); err == nil {
		t.Fatal("active report category became idle contribution authority")
	}
	state.a.currentProofCategory = ""
}
