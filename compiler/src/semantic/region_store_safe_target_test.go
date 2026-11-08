package semantic

import (
	"testing"

	"elisacore/src/ast"
)

// A complete parameter provenance can avoid scanning the callee only if the
// tracked source regions are safe for the particular destination.
func TestStoreCallRegionsSafeForTarget(t *testing.T) {
	region := &Symbol{Name: "local"}
	arg := &ast.Ident{Name: "value"}
	value := &Symbol{Name: "value", Kind: SymbolParam, Type: &SViewType{}}
	scope := NewScope(nil)
	scope.Define(value)
	a := &Analyzer{currentScope: scope, currentRegionRefs: map[*Symbol]regionRefState{value: {}}}
	if !a.storeCallRegionsSafeForTarget(arg, "") {
		t.Fatal("region-free parameter should be safe")
	}
	a.currentRegionRefs[value] = regionRefState{Deps: map[*Symbol]regionDependencyState{region: {Valid: true}}}
	if a.storeCallRegionsSafeForTarget(arg, "") {
		t.Fatal("local provenance cannot be stored through a caller-owned target")
	}
	if !a.storeCallRegionsSafeForTarget(arg, "local") {
		t.Fatal("a value stored into its own region should be safe")
	}
	value.Kind = SymbolLocal
	if a.storeCallRegionsSafeForTarget(arg, "local") {
		t.Fatal("unproven local provenance must not use the shortcut")
	}
}
