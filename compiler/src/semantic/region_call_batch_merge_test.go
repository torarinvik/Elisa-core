package semantic

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestCallElementBatchMergeMatchesSequential(t *testing.T) {
	rng := rand.New(rand.NewSource(9273))
	symbols := []*Symbol{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	var makeState func(int) regionRefState
	makeState = func(depth int) regionRefState {
		kind := rng.Intn(6)
		if kind == 0 {
			return regionRefState{Fields: map[string]regionRefState{"unknown": {}}}
		}
		if kind == 1 {
			return regionRefState{DirectParamDep: rng.Intn(6), HasDirectParamDep: true, PackedStoreSummaryKnown: true, ParamOnlySummary: true}
		}
		state := regionRefStateFromDependency(symbols[rng.Intn(len(symbols))], rng.Intn(4))
		if rng.Intn(2) == 0 {
			appendRegionParamDep(&state, rng.Intn(6))
		}
		if depth > 0 && rng.Intn(2) == 0 {
			state.Fields = map[string]regionRefState{"value": makeState(depth - 1)}
		}
		return state
	}
	for iteration := 0; iteration < 500; iteration++ {
		states := make([]regionRefState, 2+rng.Intn(6))
		for i := range states {
			states[i] = makeState(2)
		}
		want := states[0]
		known := true
		for _, next := range states[1:] {
			if merged, ok := mergeRegionRefStates(want, next); ok {
				want = merged
			} else {
				known = false
			}
		}
		got, ok := mergeCallElementStates(states)
		if ok != known || !reflect.DeepEqual(got, want) {
			t.Fatalf("batch merge differs from sequential merge at case %d", iteration)
		}
	}
}

func BenchmarkCallElementStateMerge(b *testing.B) {
	states := make([]regionRefState, 8)
	for i := range states {
		states[i] = regionRefStateFromDependency(&Symbol{}, i)
	}
	for _, batch := range []bool{false, true} {
		name := "sequential"
		if batch {
			name = "batch"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var merged regionRefState
				if batch {
					merged, _ = mergeCallElementStates(states)
				} else {
					merged = states[0]
					for _, state := range states[1:] {
						merged, _ = mergeRegionRefStates(merged, state)
					}
				}
				if len(merged.Deps) != len(states) {
					b.Fatal("lost merged dependency")
				}
			}
		})
	}
}
