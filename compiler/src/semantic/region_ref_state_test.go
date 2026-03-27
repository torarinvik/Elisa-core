package semantic

import (
	"testing"

	"llcontext/src/ast"
)

func TestCloneRegionRefStateKeepsNestedInvalidationLocal(t *testing.T) {
	region := &Symbol{Name: "scratch", Kind: SymbolRegion}
	original := regionRefState{
		Fields: map[string]regionRefState{
			"node": {
				Deps: map[*Symbol]regionDependencyState{
					region: {Generation: 1, Valid: true},
				},
			},
		},
	}

	cloned := cloneRegionRefState(original)
	updated, changed := invalidateRegionDependencyInState(cloned, region, nil, "test")
	if !changed {
		t.Fatalf("expected invalidateRegionDependencyInState to report a change")
	}

	if dep := original.Fields["node"].Deps[region]; !dep.Valid {
		t.Fatalf("expected original nested dependency to remain valid")
	}
	if dep := updated.Fields["node"].Deps[region]; dep.Valid {
		t.Fatalf("expected cloned nested dependency to be invalidated")
	}
}

func TestCloneRegionRefStateKeepsNestedStoreRemapLocal(t *testing.T) {
	from := &Symbol{Name: "from", Kind: SymbolLocal}
	to := &Symbol{Name: "to", Kind: SymbolLocal}
	localState := &BuiltinType{Name: "Local"}
	frozenState := &BuiltinType{Name: "Frozen"}
	enumType := &EnumType{Name: "Expr", Packed: true}
	originalType := &PackedEnumStoreType{Enum: enumType, State: localState}
	remappedType := &PackedEnumStoreType{Enum: enumType, State: frozenState}
	original := regionRefState{
		Fields: map[string]regionRefState{
			"node": {
				StoreDeps: map[*Symbol]packedStoreDependencyState{
					from: {Type: originalType},
				},
			},
		},
	}

	cloned := cloneRegionRefState(original)
	updated, changed := remapPackedStoreDependencyInState(cloned, from, to, remappedType)
	if !changed {
		t.Fatalf("expected remapPackedStoreDependencyInState to report a change")
	}

	if _, ok := original.Fields["node"].StoreDeps[from]; !ok {
		t.Fatalf("expected original nested store dependency to keep the original symbol")
	}
	if _, ok := original.Fields["node"].StoreDeps[to]; ok {
		t.Fatalf("expected original nested store dependency to avoid the remapped symbol")
	}
	if _, ok := updated.Fields["node"].StoreDeps[from]; ok {
		t.Fatalf("expected cloned nested store dependency to drop the original symbol")
	}
	if dep, ok := updated.Fields["node"].StoreDeps[to]; !ok || dep.Type != remappedType {
		t.Fatalf("expected cloned nested store dependency to use the remapped store type")
	}
}

func TestAssignRegionRefStateAtPathDoesNotMutateExistingNestedFields(t *testing.T) {
	original := regionRefState{
		Fields: map[string]regionRefState{
			"ret": {
				Fields: map[string]regionRefState{
					"left": {
						ParamDeps: map[int]bool{0: true},
					},
				},
			},
		},
	}

	cloned := cloneRegionRefState(original)
	updated := assignRegionRefStateAtPath(cloned, []borrowReturnAnnotationStep{{Field: "ret"}, {Field: "right"}}, regionRefState{
		ParamDeps: map[int]bool{1: true},
	})

	if _, ok := original.Fields["ret"].Fields["right"]; ok {
		t.Fatalf("expected original nested field map to stay unchanged")
	}
	if _, ok := updated.Fields["ret"].Fields["right"]; !ok {
		t.Fatalf("expected assigned state to populate the new nested field")
	}
}

func TestRegionRefStateForExprAppliesPackedStoreRemapLazily(t *testing.T) {
	from := &Symbol{Name: "from", Kind: SymbolLocal}
	to := &Symbol{Name: "to", Kind: SymbolLocal}
	value := &Symbol{Name: "value", Kind: SymbolLocal}
	localState := &BuiltinType{Name: "Local"}
	frozenState := &BuiltinType{Name: "Frozen"}
	enumType := &EnumType{Name: "Expr", Packed: true}
	originalType := &PackedEnumStoreType{Enum: enumType, State: localState}
	remappedType := &PackedEnumStoreType{Enum: enumType, State: frozenState}

	scope := NewScope(nil)
	scope.Define(value)

	a := &Analyzer{
		currentScope: scope,
		currentRegionRefs: map[*Symbol]regionRefState{
			value: {
				Fields: map[string]regionRefState{
					"node": {
						StoreDeps: map[*Symbol]packedStoreDependencyState{
							from: {Type: originalType},
						},
					},
				},
			},
		},
	}

	a.remapPackedStoreDependencies(from, to, remappedType)

	if _, ok := a.currentRegionRefs[value].Fields["node"].StoreDeps[from]; !ok {
		t.Fatalf("expected lazy remap to leave stored state untouched before lookup")
	}

	state, ok := a.regionRefStateForExpr(&ast.Ident{Name: "value"})
	if !ok {
		t.Fatalf("expected regionRefStateForExpr to resolve the stored binding")
	}
	if _, ok := state.Fields["node"].StoreDeps[from]; ok {
		t.Fatalf("expected lazy remap lookup to drop the old packed store symbol")
	}
	if dep, ok := state.Fields["node"].StoreDeps[to]; !ok || dep.Type != remappedType {
		t.Fatalf("expected lazy remap lookup to use the frozen packed store target")
	}
	if _, ok := a.currentRegionRefs[value].Fields["node"].StoreDeps[from]; ok {
		t.Fatalf("expected lookup to canonicalize the cached binding")
	}
	if dep, ok := a.currentRegionRefs[value].Fields["node"].StoreDeps[to]; !ok || dep.Type != remappedType {
		t.Fatalf("expected lookup to update the cached binding to the frozen packed store target")
	}
	if onlyFrozen, hasFrozen := regionRefStateDependsOnlyOnFrozenPackedStores(state); !onlyFrozen || !hasFrozen {
		t.Fatalf("expected canonicalized state to depend only on frozen packed stores")
	}
}

func TestCloneRegionRefStatesKeepsFieldInsertionLocal(t *testing.T) {
	value := &Symbol{Name: "value", Kind: SymbolLocal}
	a := &Analyzer{
		currentRegionRefs: map[*Symbol]regionRefState{
			value: {
				Fields: map[string]regionRefState{
					"left": {
						ParamDeps: map[int]bool{0: true},
					},
				},
			},
		},
	}

	cloned := a.cloneRegionRefStates()
	updated := assignRegionRefStateAtPath(cloned[value], []borrowReturnAnnotationStep{{Field: "right"}}, regionRefState{
		ParamDeps: map[int]bool{1: true},
	})
	cloned[value] = updated

	if _, ok := a.currentRegionRefs[value].Fields["right"]; ok {
		t.Fatalf("expected original region ref state field map to remain unchanged")
	}
	if _, ok := cloned[value].Fields["right"]; !ok {
		t.Fatalf("expected cloned region ref state field map to gain the inserted field")
	}
}
