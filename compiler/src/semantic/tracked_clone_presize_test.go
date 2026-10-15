package semantic

import (
	"testing"
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
