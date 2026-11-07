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
