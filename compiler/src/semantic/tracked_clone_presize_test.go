package semantic

import (
	"testing"

	"elisacore/src/ast"
)

// Pre-sizing the clone and merge maps must not change what they produce: a nil `seen` gets a
// fresh map, shared subtypes stay shared within one clone, and clones are independent copies.
func TestTrackedCloneAndMergePresizeKeepResults(t *testing.T) {
	a := &Analyzer{}
	inner := &StructType{Name: "Inner", Fields: map[string]Field{"x": {Name: "x", Type: &DArrayType{Elem: &BuiltinType{Name: "i64"}}}}}
	outer := &StructType{Name: "Outer", Fields: map[string]Field{
		"a": {Name: "a", Type: inner},
		"b": {Name: "b", Type: inner},
	}}
	sa, sb := &Symbol{Name: "p"}, &Symbol{Name: "q"}
	src := map[*Symbol]Type{sa: outer, sb: inner}
	cloned := a.cloneTrackedValueTypeMapWithSeen(src, nil)
	co, ok := cloned[sa].(*StructType)
	if !ok || co == outer || len(co.Fields) != 2 {
		t.Fatalf("outer must be cloned with both fields; got %#v", cloned[sa])
	}
	if co.Fields["a"].Type != co.Fields["b"].Type || co.Fields["a"].Type != cloned[sb] {
		t.Fatal("one clone pass must map a shared subtype to one clone")
	}
	if co.Fields["a"].Type == Type(inner) {
		t.Fatal("nested struct must be cloned, not aliased")
	}

	p0, p1 := &Symbol{Name: "r0"}, &Symbol{Name: "r1"}
	left := regionRefState{Deps: map[*Symbol]regionDependencyState{p0: {Valid: true, Generation: 1}},
		Fields: map[string]regionRefState{"f": {Deps: map[*Symbol]regionDependencyState{p0: {Valid: true, Generation: 1}}}}}
	right := regionRefState{Deps: map[*Symbol]regionDependencyState{p0: {Valid: true, Generation: 3}, p1: {Valid: false, Generation: 2}}}
	merged, ok := mergeRegionRefStates(left, right)
	if !ok || len(merged.Deps) != 2 || merged.Deps[p0].Generation != 3 || merged.Deps[p1].Valid || len(merged.Fields) != 1 {
		t.Fatalf("merge result changed: %+v", merged)
	}
	flat, ok := mergeFlatRegionRefStates(regionRefState{Deps: left.Deps}, right)
	if !ok || len(flat.Deps) != 2 || flat.Deps[p0].Generation != 3 {
		t.Fatalf("flat merge result changed: %+v", flat)
	}
}

func TestTrackedCloneSharesOnlyProvenPointerFreeValues(t *testing.T) {
	a := &Analyzer{}
	plain := &StructType{
		Name:   "Plain",
		Decl:   &ast.StructDecl{Fields: []ast.FieldDecl{{Name: "n"}}},
		Fields: map[string]Field{"n": {Name: "n", Type: &BuiltinType{Name: "i64"}}},
	}
	if clone := a.cloneTrackedValueType(plain); clone != plain {
		t.Fatal("pointer-free struct should be shared")
	}
	if merged, ok := a.mergeSpecializedValueTypes(plain, plain); !ok || merged != plain {
		t.Fatal("merging identical pointer-free structs should preserve the immutable value")
	}
	withRef := &StructType{
		Name:   "Borrowed",
		Decl:   &ast.StructDecl{Fields: []ast.FieldDecl{{Name: "r"}}},
		Fields: map[string]Field{"r": {Name: "r", Type: &RefType{Elem: &BuiltinType{Name: "i64"}}}},
	}
	clone, ok := a.cloneTrackedValueType(withRef).(*StructType)
	if !ok || clone == withRef || clone.Fields["r"].Type == Type(withRef.Fields["r"].Type) {
		t.Fatal("reference-bearing struct must retain isolated flow state")
	}
}

func TestMergeRegionRefStatesSharesIdenticalDependencyMaps(t *testing.T) {
	region := &Symbol{Name: "region"}
	deps := map[*Symbol]regionDependencyState{region: {Valid: true, Generation: 4}}
	state := regionRefState{Deps: deps}
	merged, ok := mergeRegionRefStates(state, state)
	if !ok || regionDependencyMapIdentity(merged.Deps) != regionDependencyMapIdentity(deps) {
		t.Fatalf("identical states should reuse their immutable dependency map: %+v", merged)
	}
	if !merged.PackedStoreSummaryKnown {
		t.Fatal("merged state must preserve the normalized packed-store summary")
	}
}
