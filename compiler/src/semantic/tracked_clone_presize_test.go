package semantic

import (
	"fmt"
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

func TestIdenticalTrackedBranchJoinRemainsIsolatedFromLaterBinding(t *testing.T) {
	a := &Analyzer{}
	declared := &RefType{Elem: &BuiltinType{Name: "i64"}, Mutable: true}
	sym := &Symbol{Name: "value", Kind: SymbolLocal, Type: declared}
	private := a.cloneTrackedValueType(declared)
	entry := map[*Symbol]Type{sym: private}
	a.currentSpecializedValueTypes = entry
	left := a.cloneSpecializedValueTypeBindings()
	right := a.cloneSpecializedValueTypeBindings()
	merged := a.mergeSpecializedValueTypeBindings(left, right)
	if merged[sym] != private {
		t.Fatal("unchanged branch join copied the private immutable graph")
	}
	merged, ok := a.intersectSpecializedValueTypeFlows(left, right)
	if !ok || merged[sym] != private {
		t.Fatal("flow intersection copied the private immutable graph")
	}
	delete(merged, sym)
	if left[sym] != private || right[sym] != private {
		t.Fatal("intersection binding map aliases a branch map")
	}
	merged[sym] = private
	a.currentSpecializedValueTypes = merged
	replacement := &RefType{Elem: declared.Elem, Mutable: false}
	a.bindTrackedValueType(sym, replacement)
	current := a.currentSpecializedValueTypes[sym].(*RefType)
	if current == replacement || current == private || current.Mutable {
		t.Fatal("later binding did not install an isolated replacement")
	}
	if !private.(*RefType).Mutable || !declared.Mutable {
		t.Fatal("later binding mutated an earlier branch or declaration")
	}
	a.verifyTrackedShares("branch join test")
}

func BenchmarkIntersectUnchangedTrackedFlows(b *testing.B) {
	a := &Analyzer{}
	leaf := &DArrayType{Elem: &BuiltinType{Name: "u8"}}
	fields := map[string]Field{}
	for i := 0; i < 64; i++ {
		name := fmt.Sprint(i)
		fields[name] = Field{Name: name, Type: leaf}
	}
	typ := &StructType{Name: "Parser", Fields: fields}
	sym := &Symbol{Name: "parser", Kind: SymbolParam, Type: typ}
	flow := map[*Symbol]Type{sym: a.cloneTrackedValueType(typ)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		merged, ok := a.intersectSpecializedValueTypeFlows(flow, flow)
		if !ok || len(merged) != 1 {
			b.Fatal("lost tracked flow")
		}
	}
}
