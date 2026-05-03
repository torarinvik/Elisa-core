package semantic

import (
	"reflect"
	"testing"
)

func TestAbstractParamOnlyRegionRefStateMatchesReturnAccumulationMerge(t *testing.T) {
	region := &Symbol{Name: "scratch", Kind: SymbolRegion}
	store := &Symbol{Name: "store", Kind: SymbolLocal}
	frozenState := &BuiltinType{Name: "Frozen"}
	enumType := &EnumType{Name: "Expr", Packed: true}
	storeType := &PackedEnumStoreType{Enum: enumType, State: frozenState}

	left := regionRefState{
		Deps: map[*Symbol]regionDependencyState{
			region: {Generation: 1, Valid: true},
		},
		Fields: map[string]regionRefState{
			"value": {
				DirectParamDep:    0,
				HasDirectParamDep: true,
			},
		},
	}
	right := regionRefState{
		StoreDeps: map[*Symbol]packedStoreDependencyState{
			store: {Type: storeType},
		},
		Fields: map[string]regionRefState{
			"value": {
				Fields: map[string]regionRefState{
					"nested": {
						DirectParamDep:    1,
						HasDirectParamDep: true,
					},
				},
			},
		},
	}

	mergedRaw, ok := mergeRegionRefStates(left, right)
	if !ok {
		t.Fatal("expected raw return provenance merge to succeed")
	}
	mergedRawSummary, ok := abstractParamOnlyRegionRefState(mergedRaw)
	if !ok {
		t.Fatal("expected abstractParamOnlyRegionRefState to preserve merged parameter provenance")
	}

	leftSummary, ok := abstractParamOnlyRegionRefState(left)
	if !ok {
		t.Fatal("expected left summary to preserve parameter provenance")
	}
	rightSummary, ok := abstractParamOnlyRegionRefState(right)
	if !ok {
		t.Fatal("expected right summary to preserve parameter provenance")
	}
	mergedSummary, ok := mergeRegionRefStates(leftSummary, rightSummary)
	if !ok {
		t.Fatal("expected summary merge to succeed")
	}

	if !reflect.DeepEqual(mergedRawSummary, mergedSummary) {
		t.Fatalf("expected merge-then-abstract to match abstract-then-merge\nraw=%#v\nsummary=%#v", mergedRawSummary, mergedSummary)
	}
}
