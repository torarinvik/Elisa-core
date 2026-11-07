package semantic

import (
	"reflect"
	"testing"
)

func TestStoreFlowFilterScalarGroupsPreservesSharedInput(t *testing.T) {
	shared := []string{"left", "n#scope", "right"}
	groups := [][]string{shared, shared, {"left", "right"}, {"n"}, nil}
	storeFlowFilterScalarGroups(groups, map[string]bool{"n": true})
	want := [][]string{{"left", "right"}, {"left", "right"}, {"left", "right"}, nil, nil}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("got %v want %v", groups, want)
	}
	if !reflect.DeepEqual(shared, []string{"left", "n#scope", "right"}) {
		t.Fatalf("shared input changed: %v", shared)
	}
}

func BenchmarkStoreFlowFilterScalarGroups(b *testing.B) {
	groups := make([][]string, 128)
	for i := range groups {
		groups[i] = []string{"source", "target", "result"}
	}
	scalars := map[string]bool{"count": true}
	for _, optimized := range []bool{false, true} {
		name := "copy"
		if optimized {
			name = "retain"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if optimized {
					storeFlowFilterScalarGroups(groups, scalars)
				} else {
					for j, group := range groups {
						var kept []string
						for _, item := range group {
							if !scalars[storeFlowBaseName(item)] {
								kept = append(kept, item)
							}
						}
						groups[j] = kept
					}
				}
			}
		})
	}
}
