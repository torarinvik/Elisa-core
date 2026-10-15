package semantic

import "testing"

// typeCanContainRegionRefs is memoized only after type shapes are frozen, and the memoized answer
// equals a fresh walk. Before the freeze a struct whose fields are still being populated must be
// re-walked (no stale `false`).
func TestTypeCanContainRegionRefsMemo(t *testing.T) {
	a := &Analyzer{}
	i64 := &BuiltinType{Name: "i64"}
	plain := &StructType{Name: "Plain", Fields: map[string]Field{"n": {Name: "n", Type: i64}}}
	growing := &StructType{Name: "Growing", Fields: map[string]Field{}}
	self := &StructType{Name: "Node", Fields: map[string]Field{}}
	self.Fields["next"] = Field{Name: "next", Type: &OptionalType{Value: self}}
	self.Fields["items"] = Field{Name: "items", Type: &SViewType{}}

	if a.typeCanContainRegionRefs(growing, map[string]bool{}) {
		t.Fatal("an empty struct holds no region refs")
	}
	growing.Fields["r"] = Field{Name: "r", Type: &RefType{Elem: i64}}
	if !a.typeCanContainRegionRefs(growing, map[string]bool{}) {
		t.Fatal("before the freeze, a struct populated later must be re-walked")
	}
	if a.regionRefsMemo != nil {
		t.Fatal("nothing may be memoized before typeShapesFrozen")
	}

	a.typeShapesFrozen = true
	for _, typ := range []Type{plain, growing, self, i64, &ArrayType{Elem: plain}, &DictType{Key: i64, Value: self}} {
		want := a.typeCanContainRegionRefsWalk(typ, map[string]bool{})
		for round := 0; round < 2; round++ {
			if got := a.typeCanContainRegionRefs(typ, map[string]bool{}); got != want {
				t.Fatalf("%v round %d: memo %v, walk %v", typ, round, got, want)
			}
		}
		if cached, ok := a.regionRefsMemo[typ]; !ok || cached != want {
			t.Fatalf("%v: want memoized %v after the freeze; got %v %v", typ, want, cached, ok)
		}
	}
	// A non-empty `seen` (a call from inside another walk) bypasses the memo.
	if a.typeCanContainRegionRefs(self, map[string]bool{self.String(): true}) {
		t.Fatal("an already-visited type must answer false inside a walk, not the memoized true")
	}
}
