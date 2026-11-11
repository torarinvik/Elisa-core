package semantic

import "testing"

func TestPackedStorePropagationAcrossRecursiveCalls(t *testing.T) {
	entry, left, right, leaf, unrelated := &FuncType{}, &FuncType{}, &FuncType{}, &FuncType{}, &FuncType{}
	order := []*FuncType{entry, left, right, leaf, unrelated}
	callees := map[*FuncType]map[*FuncType]bool{
		entry: {left: true},
		left:  {right: true},
		right: {left: true, leaf: true},
	}
	tree := &EnumType{Name: "Tree", Packed: true}
	needs := map[*FuncType]map[string]*EnumType{leaf: {"Tree": tree}}
	builds := map[*FuncType]map[string]string{leaf: {"Tree": "makeLeaf"}}
	propagatePackedStoreNeeds(order, callees, needs, builds)
	for _, fn := range order[:4] {
		if needs[fn]["Tree"] != tree || builds[fn]["Tree"] != "makeLeaf" {
			t.Fatalf("missing transitive store requirement or construction witness")
		}
	}
	if len(needs[unrelated]) != 0 || len(builds[unrelated]) != 0 {
		t.Fatal("unrelated function acquired store facts")
	}
}
