package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func regionRefMapIdentity(refs map[*Symbol]regionRefState) uintptr {
	if refs == nil {
		return 0
	}
	return reflect.ValueOf(refs).Pointer()
}

func TestBorrowedRegionRefMapCopiesOnlyOnWrite(t *testing.T) {
	sym := &Symbol{Name: "value", Kind: SymbolLocal}
	region := &Symbol{Name: "arena", Kind: SymbolLocal}
	originalState := regionRefStateFromDependency(region, 1)
	nextState := regionRefStateFromDependency(region, 2)
	original := map[*Symbol]regionRefState{sym: originalState}
	a := &Analyzer{currentRegionRefs: original}

	a.borrowRegionRefStates()
	if !a.currentRegionRefsBorrowed || regionRefMapIdentity(a.currentRegionRefs) != regionRefMapIdentity(original) {
		t.Fatal("read-only scope did not retain the borrowed map")
	}
	a.setCurrentRegionRefState(sym, nextState)
	if a.currentRegionRefsBorrowed || regionRefMapIdentity(a.currentRegionRefs) == regionRefMapIdentity(original) {
		t.Fatal("first write did not take ownership of a copy")
	}
	if got := original[sym].Deps[region]; got.Generation != 1 {
		t.Fatalf("first write changed the caller's map: %#v", got)
	}
	if got := a.currentRegionRefs[sym].Deps[region]; got.Generation != 2 {
		t.Fatalf("owned map missed the write: %#v", got)
	}

	owned := regionRefMapIdentity(a.currentRegionRefs)
	a.setCurrentRegionRefState(sym, originalState)
	if regionRefMapIdentity(a.currentRegionRefs) != owned {
		t.Fatal("subsequent writes copied an already-owned map")
	}
}

func TestBorrowedRegionRefDeleteCopiesOnlyWhenPresent(t *testing.T) {
	sym := &Symbol{Name: "value", Kind: SymbolLocal}
	state := regionRefStateFromDependency(&Symbol{Name: "arena", Kind: SymbolLocal}, 1)
	original := map[*Symbol]regionRefState{sym: state}
	a := &Analyzer{currentRegionRefs: original}

	a.borrowRegionRefStates()
	a.deleteCurrentRegionRefState(&Symbol{Name: "missing", Kind: SymbolLocal})
	if !a.currentRegionRefsBorrowed || regionRefMapIdentity(a.currentRegionRefs) != regionRefMapIdentity(original) {
		t.Fatal("deleting an absent key copied the borrowed map")
	}
	a.deleteCurrentRegionRefState(sym)
	if a.currentRegionRefsBorrowed || regionRefMapIdentity(a.currentRegionRefs) == regionRefMapIdentity(original) {
		t.Fatal("deleting a present key did not take ownership of a copy")
	}
	if _, ok := original[sym]; !ok {
		t.Fatal("delete changed the caller's map")
	}
	if _, ok := a.currentRegionRefs[sym]; ok {
		t.Fatal("owned map retained the deleted key")
	}
}

func TestRegionRefBlockAssignmentKeepsCallerMapIsolated(t *testing.T) {
	const sourceText = "def base() -> i64:\n    return 0\n"
	_, retained := AnalyzeRetained(retainedParse(t, sourceText), retainedParse(t, sourceText), []byte(sourceText), AnalyzeOptions{})
	if retained == nil {
		t.Fatal("analysis not retained")
	}
	typ := &BuiltinType{Name: "i64"}
	target := &Symbol{Name: "target", Kind: SymbolLocal, Type: typ, Mutable: true}
	source := &Symbol{Name: "source", Kind: SymbolLocal, Type: typ, Mutable: true}
	oldRegion := &Symbol{Name: "old", Kind: SymbolLocal}
	newRegion := &Symbol{Name: "new", Kind: SymbolLocal}
	scope := NewScope(nil)
	scope.Define(target)
	scope.Define(source)
	caller := map[*Symbol]regionRefState{
		target: regionRefStateFromDependency(oldRegion, 1),
		source: regionRefStateFromDependency(newRegion, 2),
	}
	a := retained.a
	a.currentScope = scope
	a.currentRegionRefs = caller
	a.currentRegionRefsBorrowed = false
	originalMap := regionRefMapIdentity(caller)

	a.analyzeBlockWithRegionCloneCapture([]ast.Stmt{
		&ast.AssignStmt{Target: &ast.Ident{Name: "target"}, Value: &ast.Ident{Name: "source"}},
	}, scope)

	if regionRefMapIdentity(a.currentRegionRefs) != originalMap {
		t.Fatal("branch did not restore the caller's region-ref map")
	}
	if got := caller[target].Deps[oldRegion]; !got.Valid || got.Generation != 1 {
		t.Fatalf("branch assignment changed caller target provenance: %#v", got)
	}
	if got := caller[target].Deps[newRegion]; got.Valid {
		t.Fatalf("branch assignment leaked into caller target provenance: %#v", got)
	}
	if a.currentRegionRefsBorrowed {
		t.Fatal("branch return left caller map borrowed")
	}
}
