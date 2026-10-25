package semantic

import (
	"testing"

	"elisacore/src/ast"
)

// Program ownership means no LOCAL lifetime dependency. It must not turn a
// precisely known element into an unknown one merely because the global owner
// has no lexical currentRegions entry.
func TestProgramRecordElementStorageRetainsKnownNoLocalDependencies(t *testing.T) {
	const src = `global region source
struct Subset:
    restricted: bool
    variants: darray[sview @source] @source
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "program_record_element_state.elisa", src, AnalyzeOptions{})
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatal(errors)
	}
	typ := result.NamedTypes["Subset"]
	if typ == nil {
		t.Fatal("missing Subset type")
	}
	a := result.analyzer
	a.currentScope = NewScope(a.globalScope)
	a.currentRegions = map[*Symbol]regionState{}
	symbol := &Symbol{Name: "program_record", Kind: SymbolLocal, Type: typ}
	if _, ok := a.currentScope.Define(symbol); !ok {
		t.Fatal("failed to define fixture binding")
	}
	expr := &ast.Ident{Name: symbol.Name}
	a.exprTypes[expr] = typ

	state, present, known := a.elementStorageState(expr)
	if !known || !present {
		t.Fatalf("wholly program-owned element lost precise provenance: present=%v known=%v state=%+v", present, known, state)
	}
	if names := liveLocalRegionDependencyNames(state); len(names) != 0 {
		t.Fatalf("program owner became a local dependency: %v", names)
	}
	if hasRegionProvenance(state) {
		t.Fatalf("program-owned local acquired borrowed/local provenance: %+v", state)
	}
}
