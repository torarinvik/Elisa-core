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

func TestCloneRegionRefStatesKeepsDependencyInvalidationLocal(t *testing.T) {
	value := &Symbol{Name: "value", Kind: SymbolLocal}
	region := &Symbol{Name: "scratch", Kind: SymbolRegion}
	a := &Analyzer{
		currentRegionRefs: map[*Symbol]regionRefState{
			value: {
				Deps: map[*Symbol]regionDependencyState{
					region: {Generation: 1, Valid: true},
				},
			},
		},
	}

	cloned := a.cloneRegionRefStates()
	updated, changed := invalidateRegionDependencyInState(cloned[value], region, nil, "test")
	if !changed {
		t.Fatalf("expected invalidateRegionDependencyInState to report a change")
	}
	cloned[value] = updated

	if dep := a.currentRegionRefs[value].Deps[region]; !dep.Valid {
		t.Fatalf("expected original region dependency to remain valid")
	}
	if dep := cloned[value].Deps[region]; dep.Valid {
		t.Fatalf("expected cloned region dependency to be invalidated")
	}
}

func TestCloneRegionRefStatesKeepsStoreRemapLocal(t *testing.T) {
	value := &Symbol{Name: "value", Kind: SymbolLocal}
	from := &Symbol{Name: "from", Kind: SymbolLocal}
	to := &Symbol{Name: "to", Kind: SymbolLocal}
	localState := &BuiltinType{Name: "Local"}
	frozenState := &BuiltinType{Name: "Frozen"}
	enumType := &EnumType{Name: "Expr", Packed: true}
	originalType := &PackedEnumStoreType{Enum: enumType, State: localState}
	remappedType := &PackedEnumStoreType{Enum: enumType, State: frozenState}
	a := &Analyzer{
		currentRegionRefs: map[*Symbol]regionRefState{
			value: {
				StoreDeps: map[*Symbol]packedStoreDependencyState{
					from: {Type: originalType},
				},
			},
		},
	}

	cloned := a.cloneRegionRefStates()
	updated, changed := remapPackedStoreDependencyInState(cloned[value], from, to, remappedType)
	if !changed {
		t.Fatalf("expected remapPackedStoreDependencyInState to report a change")
	}
	cloned[value] = updated

	if _, ok := a.currentRegionRefs[value].StoreDeps[from]; !ok {
		t.Fatalf("expected original store dependency to keep the original symbol")
	}
	if _, ok := a.currentRegionRefs[value].StoreDeps[to]; ok {
		t.Fatalf("expected original store dependency to avoid the remapped symbol")
	}
	if _, ok := cloned[value].StoreDeps[from]; ok {
		t.Fatalf("expected cloned store dependency to drop the original symbol")
	}
	if dep, ok := cloned[value].StoreDeps[to]; !ok || dep.Type != remappedType {
		t.Fatalf("expected cloned store dependency to use the remapped store type")
	}
}

func TestMergeFlatRegionRefStatesKeepsDependencyInvalidationLocal(t *testing.T) {
	region := &Symbol{Name: "scratch", Kind: SymbolRegion}
	left := regionRefState{
		Deps: map[*Symbol]regionDependencyState{
			region: {Generation: 1, Valid: true},
		},
	}
	right := regionRefState{
		ParamDeps: map[int]bool{0: true},
	}

	merged, ok := mergeFlatRegionRefStates(left, right)
	if !ok {
		t.Fatalf("expected mergeFlatRegionRefStates to merge flat provenance states")
	}
	updated, changed := invalidateRegionDependencyInState(merged, region, nil, "test")
	if !changed {
		t.Fatalf("expected invalidateRegionDependencyInState to report a change")
	}

	if dep := left.Deps[region]; !dep.Valid {
		t.Fatalf("expected original flat dependency to remain valid")
	}
	if dep := updated.Deps[region]; dep.Valid {
		t.Fatalf("expected merged flat dependency to be invalidated")
	}
	if !updated.ParamDeps[0] {
		t.Fatalf("expected merged flat state to keep parameter provenance")
	}
}

func TestMergeRegionRefStatesWithExplicitFieldsKeepsOverlayMutationLocal(t *testing.T) {
	overlay := regionRefState{
		Fields: map[string]regionRefState{
			"inner": {
				ParamDeps: map[int]bool{0: true},
			},
		},
	}

	merged, ok := mergeRegionRefStatesWithExplicitFields([]regionRefState{overlay}, map[string]regionRefState{
		"slot": overlay,
	})
	if !ok {
		t.Fatalf("expected mergeRegionRefStatesWithExplicitFields to keep overlay provenance")
	}
	updated := assignRegionRefStateAtPath(merged, []borrowReturnAnnotationStep{{Field: "slot"}, {Field: "extra"}}, regionRefState{
		ParamDeps: map[int]bool{1: true},
	})

	if _, ok := overlay.Fields["extra"]; ok {
		t.Fatalf("expected original overlay field map to remain unchanged")
	}
	if _, ok := updated.Fields["slot"].Fields["extra"]; !ok {
		t.Fatalf("expected merged overlay field map to gain the inserted field")
	}
}

func TestInstantiateReturnProvenanceKeepsOverlayMutationLocal(t *testing.T) {
	left := &Symbol{Name: "left", Kind: SymbolLocal}
	right := &Symbol{Name: "right", Kind: SymbolLocal}
	scope := NewScope(nil)
	scope.Define(left)
	scope.Define(right)

	a := &Analyzer{
		currentScope: scope,
		currentRegionRefs: map[*Symbol]regionRefState{
			left: {
				ParamDeps: map[int]bool{9: true},
			},
			right: {
				Fields: map[string]regionRefState{
					"inner": {
						ParamDeps: map[int]bool{7: true},
					},
				},
			},
		},
	}

	summary := regionRefState{
		ParamDeps: map[int]bool{0: true},
		Fields: map[string]regionRefState{
			"slot": {
				ParamDeps: map[int]bool{1: true},
			},
		},
	}

	instantiated, ok := a.instantiateReturnProvenance(summary, []ast.Expr{
		&ast.Ident{Name: "left"},
		&ast.Ident{Name: "right"},
	})
	if !ok {
		t.Fatalf("expected instantiateReturnProvenance to resolve explicit field overlays")
	}
	if !instantiated.ParamDeps[9] {
		t.Fatalf("expected instantiated return provenance to include top-level argument provenance")
	}

	updated := assignRegionRefStateAtPath(instantiated, []borrowReturnAnnotationStep{{Field: "slot"}, {Field: "extra"}}, regionRefState{
		ParamDeps: map[int]bool{2: true},
	})

	if _, ok := a.currentRegionRefs[right].Fields["extra"]; ok {
		t.Fatalf("expected original argument field map to remain unchanged")
	}
	if _, ok := updated.Fields["slot"].Fields["extra"]; !ok {
		t.Fatalf("expected instantiated overlay field map to gain the inserted field")
	}
}
