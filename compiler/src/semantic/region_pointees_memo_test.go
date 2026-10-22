package semantic

import (
	"fmt"
	"maps"
	"testing"
)

func sameRegionPointees(a, b regionPointees) bool {
	return a.wildcard == b.wildcard && maps.Equal(a.keys, b.keys)
}

func TestRegionPointeesMemoMatchesFullTraversal(t *testing.T) {
	scalar := &BuiltinType{Name: "i64"}
	node := &StructType{Name: "Node", Fields: map[string]Field{}}
	node.Fields["next"] = Field{Name: "next", Type: &RefType{Elem: node}}
	node.Fields["text"] = Field{Name: "text", Type: &SViewType{}}
	packed := &EnumType{Name: "Handle", Packed: true, Variants: []*EnumVariant{{Name: "Text", Payload: []Type{&SViewType{}}}}}
	types := []Type{scalar, node, packed, &TypeParamType{Name: "T"}, &StructType{Name: "Opaque", Store: true}, &DArrayType{Elem: node}, &DArrayType{Elem: scalar},
		&DictType{Key: &SViewType{}, Value: node}, &SetType{Elem: node}, &OptionalType{Value: node}, nil}
	a := &Analyzer{typeShapesFrozen: true}
	for _, typ := range types {
		for round := 0; round < 2; round++ {
			if got, want := a.argumentStorePointeesOf(typ), argumentStorePointees(typ); !sameRegionPointees(got, want) {
				t.Fatalf("argument %T: got %#v, want %#v", typ, got, want)
			}
			if got, want := a.containerElementPointeesOf(typ), containerElementPointees(typ); !sameRegionPointees(got, want) {
				t.Fatalf("container %T: got %#v, want %#v", typ, got, want)
			}
		}
	}
	// A plain integer darray's backing slots are reachable from the argument,
	// but the stored integers themselves carry no references. Keep modes distinct.
	ints := &DArrayType{Elem: scalar}
	if !a.argumentStorePointeesOf(ints).keys["i64"] || len(a.containerElementPointeesOf(ints).keys) != 0 {
		t.Fatal("argument backing storage and element reachability were conflated")
	}
}

func TestRegionPointeesMemoWaitsForFinalShapes(t *testing.T) {
	a := &Analyzer{}
	typ := &StructType{Name: "Growing", Fields: map[string]Field{}}
	if got := a.argumentStorePointeesOf(typ); len(got.keys) != 0 || got.wildcard {
		t.Fatal("empty struct unexpectedly carries references")
	}
	typ.Fields["text"] = Field{Name: "text", Type: &SViewType{}}
	if !a.argumentStorePointeesOf(typ).keys["u8"] || a.regionPointeesMemo != nil {
		t.Fatal("unfrozen type reused a stale result")
	}
	a.typeShapesFrozen = true
	a.argumentStorePointeesOf(typ)
	if len(a.regionPointeesMemo) != 1 {
		t.Fatal("completed root was not cached after freezing")
	}
}

func TestRegionPointeesMemoIsPrivateToBodyWorker(t *testing.T) {
	typ := &SViewType{}
	base := &Analyzer{typeShapesFrozen: true}
	base.argumentStorePointeesOf(typ)
	worker := cloneAnalyzerValueForBodyWorker(base).Addr().Interface().(*Analyzer)
	if worker.regionPointeesMemo != nil {
		t.Fatal("worker inherited a potentially mutable memo")
	}
	worker.argumentStorePointeesOf(typ)
	key := regionPointeesMemoKey{typ: typ}
	worker.regionPointeesMemo[key].keys["worker-only"] = true
	if base.regionPointeesMemo[key].keys["worker-only"] {
		t.Fatal("worker memo aliases the base analyzer")
	}
	if !parallelBodyWorkerLocalOnlyMap("regionPointeesMemo") {
		t.Fatal("worker memo can leak through root merge")
	}
}

func BenchmarkRegionPointeesRepeatedArgument(b *testing.B) {
	leaf := &StructType{Name: "Record", Fields: map[string]Field{
		"text": {Name: "text", Type: &SViewType{}},
	}}
	root := &StructType{Name: "Root", Fields: map[string]Field{}}
	for i := 0; i < 64; i++ {
		name := fmt.Sprint("field", i)
		root.Fields[name] = Field{Name: name, Type: &DArrayType{Elem: leaf}}
	}
	for _, memo := range []bool{false, true} {
		b.Run(fmt.Sprint("memo=", memo), func(b *testing.B) {
			a := &Analyzer{typeShapesFrozen: true}
			a.argumentStorePointeesOf(root)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var result regionPointees
				if memo {
					result = a.argumentStorePointeesOf(root)
				} else {
					result = argumentStorePointees(root)
				}
				if !result.keys["u8"] {
					b.Fatal("lost nested sview storage")
				}
			}
		})
	}
}

func TestRetainedUpdateClearsRegionPointeesMemo(t *testing.T) {
	const before = "def value() -> i64:\n    return 1\n"
	const after = "def value() -> i64:\n    return 2\n"
	_, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
	if retained == nil {
		t.Fatal("analysis was not retained")
	}
	// A type inferred only inside an earlier body must not be retained by the cache
	// after that body has been replaced.
	oldType := &DArrayType{Elem: &SViewType{}}
	retained.a.argumentStorePointeesOf(oldType)
	key := regionPointeesMemoKey{typ: oldType}
	if _, ok := retained.a.regionPointeesMemo[key]; !ok {
		t.Fatal("fixture did not populate the cache")
	}
	_, err := retained.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := retained.a.regionPointeesMemo[key]; ok {
		t.Fatal("retained update kept the previous body type graph")
	}
}
