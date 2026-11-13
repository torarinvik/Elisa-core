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

func TestAnalysisBuildReconstructsTypePredicates(t *testing.T) {
	const source = "def main() -> i64:\n    return 0\n"
	build, err := BeginAnalysisBuild(retainedParse(t, source), nil, []byte(source), AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := build.retained.a
	typ := &RefType{Elem: &BuiltinType{Name: "i64"}}
	want := a.typeCanContainRegionRefs(typ, nil)
	if len(a.regionRefsMemo) == 0 {
		t.Fatal("fixture did not warm the predicate memo")
	}
	data, err := build.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := a.regionRefsMemo[typ]; !ok || got != want {
		t.Fatal("checkpoint changed the live predicate memo")
	}
	restored, err := RestoreAnalysisBuild(data)
	if err != nil {
		t.Fatal(err)
	}
	cold := restored.retained.a
	if cold.regionRefsMemo != nil {
		t.Fatal("checkpoint included the predicate memo")
	}
	if got := cold.typeCanContainRegionRefs(typ, nil); got != want {
		t.Fatalf("rebuilt predicate = %v, want %v", got, want)
	}
}

func TestAnalysisBuildReconstructsStoreScanTypePredicates(t *testing.T) {
	const source = "def main() -> i64:\n    return 0\n"
	build, err := BeginAnalysisBuild(retainedParse(t, source), nil, []byte(source), AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := build.retained.a
	typ := &ArrayType{Elem: &BuiltinType{Name: "uintptr"}}
	address := a.storeScanTypeMayEncodeAddress(typ, map[Type]bool{})
	plain := a.storeScanTypeIsPlainValue(typ)
	noStorage := a.storeScanHoldsNoRegionStorage(typ)
	viewOnly := a.storeScanViewOnlyCached(typ)
	shares := a.storeScanSharesCached(typ)
	// Program-lived classification only runs when a program region exists;
	// seed its derived memo to verify it is not serialized.
	a.storeScanProgramLivedMemo = map[Type]bool{typ: false}
	if len(a.storeScanAddressMemo) == 0 || len(a.storeScanPlainValueMemo) == 0 {
		t.Fatal("fixture did not warm the store-scan predicate memos")
	}
	data, err := build.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.storeScanAddressMemo) == 0 || len(a.storeScanPlainValueMemo) == 0 || len(a.storeScanProgramLivedMemo) == 0 {
		t.Fatal("checkpoint changed live store-scan predicate memos")
	}
	if len(a.storeScanNoStorageCache) == 0 || len(a.storeScanViewOnlyCache) == 0 || len(a.storeScanSharesCache) == 0 {
		t.Fatal("checkpoint changed live store-scan classification caches")
	}
	restored, err := RestoreAnalysisBuild(data)
	if err != nil {
		t.Fatal(err)
	}
	cold := restored.retained.a
	if cold.storeScanAddressMemo != nil || cold.storeScanPlainValueMemo != nil || cold.storeScanProgramLivedMemo != nil {
		t.Fatal("checkpoint serialized derived store-scan predicate memos")
	}
	if cold.storeScanNoStorageCache != nil || cold.storeScanViewOnlyCache != nil || cold.storeScanSharesCache != nil {
		t.Fatal("checkpoint serialized derived store-scan classification caches")
	}
	if cold.storeScanTypeMayEncodeAddress(typ, map[Type]bool{}) != address || cold.storeScanTypeIsPlainValue(typ) != plain {
		t.Fatal("recomputed store-scan predicates changed")
	}
	if cold.storeScanHoldsNoRegionStorage(typ) != noStorage || cold.storeScanViewOnlyCached(typ) != viewOnly || cold.storeScanSharesCached(typ) != shares {
		t.Fatal("recomputed store-scan classifications changed")
	}
}
