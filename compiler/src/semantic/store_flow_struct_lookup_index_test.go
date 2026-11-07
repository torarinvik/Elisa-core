package semantic

import (
	"fmt"
	"testing"
)

func TestStoreFlowStructLookupIndexMatchesUncached(t *testing.T) {
	shared := &StructType{Name: "Shared"}
	named := map[string]Type{
		"A.Shared": shared, "B.Shared": shared,
		"A.Conflict": &StructType{Name: "A.Conflict"}, "B.Conflict": &StructType{Name: "B.Conflict"},
		"A.Other": &StructType{Name: "A.Other"}, "B.Other": &BuiltinType{Name: "Other"},
		"A.Nil": (*StructType)(nil),
	}
	locals := map[string]string{}
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("Item%d", i)
		named["Module."+name] = &StructType{Name: name}
		locals[name] = name
	}
	for _, name := range []string{"Shared", "Conflict", "Other", "Nil", "Missing"} {
		locals[name] = name
	}
	reference := &Analyzer{namedTypes: named}
	indexed := &Analyzer{namedTypes: named, storeFlowNamedTypes: locals, storeFlowStructLookup: map[string]Type{}}
	for i := 0; i < 10; i++ {
		indexed.storeFlowStructNamed(fmt.Sprintf("Item%d", i))
	}
	if !indexed.storeFlowStructLookupComplete {
		t.Fatal("index was not built")
	}
	for _, name := range []string{"Shared", "Conflict", "Other", "Nil", "Missing", "Item11", "Unindexed"} {
		if got, want := indexed.storeFlowStructNamed(name), reference.storeFlowStructNamed(name); got != want {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
	}
}

func TestStoreFlowStructLookupLargeTableIndexesEarly(t *testing.T) {
	named := map[string]Type{}
	locals := map[string]string{}
	for i := 0; i < 256; i++ {
		name := fmt.Sprintf("Item%d", i)
		named["Module."+name] = &StructType{Name: name}
		locals[name] = name
	}
	named["Other.Item3"] = &StructType{Name: "Conflict"}
	indexed := &Analyzer{namedTypes: named, storeFlowNamedTypes: locals, storeFlowStructLookup: map[string]Type{}}
	reference := &Analyzer{namedTypes: named}
	for _, name := range []string{"Item0", "Item1", "Item2"} {
		if got, want := indexed.storeFlowStructNamed(name), reference.storeFlowStructNamed(name); got != want {
			t.Fatalf("%s mismatch", name)
		}
	}
	if !indexed.storeFlowStructLookupComplete {
		t.Fatal("large table was not indexed after the second miss")
	}
	for _, name := range []string{"Item3", "Item255", "missing"} {
		if got, want := indexed.storeFlowStructNamed(name), reference.storeFlowStructNamed(name); got != want {
			t.Fatalf("%s mismatch", name)
		}
	}
}

func TestStoreFlowFrozenStructLookupMatchesUncached(t *testing.T) {
	shared := &StructType{Name: "Shared"}
	named := map[string]Type{
		"A.Shared": shared, "B.Shared": shared,
		"A.Conflict": &StructType{Name: "A"}, "B.Conflict": &StructType{Name: "B"},
		"A.NonStruct": &BuiltinType{Name: "NonStruct"}, "B.NonStruct": &StructType{Name: "NonStruct"},
		"A.Nil": (*StructType)(nil), "A.Unique": &StructType{Name: "Unique"},
	}
	reference := &Analyzer{namedTypes: named}
	worker := &Analyzer{namedTypes: named, typeShapesFrozen: true, parallel: &parallelWorkerState{}}
	for _, name := range []string{"Shared", "Conflict", "NonStruct", "Nil", "Unique", "Missing"} {
		if got, want := worker.storeFlowStructNamed(name), reference.storeFlowStructNamed(name); got != want {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
	}
	if worker.storeFlowFrozenStructLookup == nil {
		t.Fatal("frozen index was not created")
	}
	worker.clearRetainedTypeMemos()
	delete(named, "A.Unique")
	if got := worker.storeFlowStructNamed("Unique"); got != nil {
		t.Fatal("retained edit left stale index")
	}
}
