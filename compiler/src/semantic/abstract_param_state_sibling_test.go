package semantic

import "testing"

// Two fields of the SAME type must each get the full abstract state. The `seen` set
// exists only to stop a recursive type from descending forever; when it survived past
// the field it guarded, the second field of that type got the truncated answer — and
// since StructType.Fields is a map, WHICH field got truncated changed run to run. A
// function returning `scope.origins[i]` then reported `isolated` (it borrows nothing)
// on some runs and the correct `alias_params=[0]` on others.
func TestAbstractParamRegionRefStateExpandsEverySameTypedField(t *testing.T) {
	a := &Analyzer{}
	elems := &DArrayType{Elem: &SViewType{}, Shape: &WildcardShape{}, SurfaceName: "darray"}
	scope := &StructType{
		Name: "Scope",
		Fields: map[string]Field{
			"names":   {Name: "names", Type: elems},
			"origins": {Name: "origins", Type: elems},
		},
	}
	state, ok := a.abstractParamRegionRefState(&RefType{Elem: scope, State: RefStateNonNull}, 0, map[string]bool{})
	if !ok {
		t.Fatalf("abstractParamRegionRefState returned ok=false for Scope&")
	}
	for _, name := range []string{"names", "origins"} {
		fieldState, ok := state.Fields[name]
		if !ok {
			t.Fatalf("field %q missing from the abstract param state", name)
		}
		if _, ok := fieldState.Fields[regionAnyIndexFieldKey()]; !ok {
			t.Fatalf("field %q has no element state: indexing it would look isolated", name)
		}
	}
}

// The guard it replaced still has to hold: a self-referential type must terminate.
func TestAbstractParamRegionRefStateTerminatesOnRecursiveType(t *testing.T) {
	a := &Analyzer{}
	node := &StructType{Name: "Node", Fields: map[string]Field{}}
	node.Fields["next"] = Field{Name: "next", Type: &RefType{Elem: node, State: RefStateNullable}}
	node.Fields["text"] = Field{Name: "text", Type: &SViewType{}}
	if _, ok := a.abstractParamRegionRefState(&RefType{Elem: node, State: RefStateNonNull}, 0, map[string]bool{}); !ok {
		t.Fatalf("abstractParamRegionRefState returned ok=false for Node&")
	}
}

func TestAbstractParamRegionRefStateMemoClonesMutableDependencies(t *testing.T) {
	a := &Analyzer{typeShapesFrozen: true}
	typ := &RefType{Elem: &DArrayType{Elem: &SViewType{}, Shape: &WildcardShape{}, SurfaceName: "darray"}, State: RefStateNonNull}
	first, ok := a.abstractParamRegionRefState(typ, 70, map[string]bool{})
	if !ok {
		t.Fatal("abstractParamRegionRefState returned ok=false for a region-reference type")
	}
	if len(a.abstractParamRegionRefMemo) != 1 {
		t.Fatalf("expected one root result in the memo, got %d", len(a.abstractParamRegionRefMemo))
	}
	first.ParamDeps.Add(70)
	second, ok := a.abstractParamRegionRefState(typ, 70, map[string]bool{})
	if !ok {
		t.Fatal("memoized abstractParamRegionRefState returned ok=false")
	}
	if !second.ParamDeps.IsEmpty() {
		t.Fatal("mutating the first result changed the cached parameter dependencies")
	}
}

func TestAbstractParamRegionRefStateForBindingReusesFrozenDependencies(t *testing.T) {
	region := &Symbol{Name: "region"}
	deps := map[*Symbol]regionDependencyState{region: {Valid: true, Generation: 2}}
	typ := &RefType{Elem: &DArrayType{Elem: &SViewType{}, Shape: &WildcardShape{}, SurfaceName: "darray"}, State: RefStateNonNull}
	a := &Analyzer{
		typeShapesFrozen: true,
		abstractParamRegionRefMemo: map[abstractParamRegionRefKey]regionRefState{
			{typeOf: typ, paramIndex: 0}: {Deps: deps, PackedStoreSummaryKnown: true},
		},
		currentRegionRefs: map[*Symbol]regionRefState{},
	}
	state, ok := a.abstractParamRegionRefStateForBinding(typ, 0)
	if !ok || regionDependencyMapIdentity(state.Deps) != regionDependencyMapIdentity(deps) {
		t.Fatal("parameter binding should reuse the frozen dependency summary")
	}
	sym := &Symbol{Name: "value", Kind: SymbolParam, Type: typ}
	a.recordResolvedRegionRefBinding(sym, state)
	stored := a.currentRegionRefs[sym]
	if regionDependencyMapIdentity(stored.Deps) != regionDependencyMapIdentity(deps) {
		t.Fatal("recording the binding should preserve copy-on-write dependency maps")
	}
}
