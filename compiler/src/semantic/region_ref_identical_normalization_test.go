package semantic

import (
	"reflect"
	"testing"
)

func TestIdenticalRegionRefMergeNormalizesEmptyMaps(t *testing.T) {
	for mask := 1; mask < 8; mask++ {
		state := regionRefState{DirectParamDep: 0, HasDirectParamDep: true, PackedStoreSummaryKnown: true, ParamOnlySummary: true}
		if mask&1 != 0 {
			state.Fields = map[string]regionRefState{}
		}
		if mask&2 != 0 {
			state.Deps = map[*Symbol]regionDependencyState{}
		}
		if mask&4 != 0 {
			state.StoreDeps = map[*Symbol]packedStoreDependencyState{}
		}
		want, ok := mergeRegionRefStates(state)
		if !ok {
			t.Fatal("missing provenance")
		}
		got, ok := mergeRegionRefStates(state, state)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Fatalf("empty map mask %d: identical merge differs from normalized state:\ngot %#v\nwant %#v", mask, got, want)
		}
	}
}
