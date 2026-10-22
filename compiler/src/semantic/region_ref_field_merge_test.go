package semantic

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
)

func sharedFieldMergeFixture(width int) (regionRefState, regionRefState, map[string]regionRefState) {
	backing := &Symbol{Name: "backing"}
	leaf := regionRefStateFromDependency(backing, 1)
	fields := map[string]regionRefState{}
	for i := 0; i < width; i++ {
		fields["field_"+strconv.Itoa(i)] = leaf
	}
	left := regionRefStateFromDependency(&Symbol{Name: "left"}, 1)
	right := regionRefStateFromDependency(&Symbol{Name: "right"}, 2)
	left.Fields, right.Fields = fields, fields
	return left, right, fields
}

func BenchmarkMergeSharedRegionFields(b *testing.B) {
	left, right, _ := sharedFieldMergeFixture(64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		merged, ok := mergeRegionRefStates(left, right)
		if !ok || len(merged.Fields) != 64 || len(merged.Deps) != 2 {
			b.Fatal("merged provenance changed")
		}
	}
}

func TestMergeSharedRegionFieldsKeepsDependencies(t *testing.T) {
	left, right, fields := sharedFieldMergeFixture(16)
	merged, ok := mergeRegionRefStates(left, right)
	if !ok || len(merged.Deps) != 2 {
		t.Fatal("outer dependencies were not unioned")
	}
	if regionRefFieldsIdentity(merged.Fields) != regionRefFieldsIdentity(fields) {
		t.Fatal("identical immutable leaves were copied")
	}
	for name, leaf := range fields {
		want, valid := mergeFlatRegionRefStates(leaf, leaf)
		if !valid || !reflect.DeepEqual(merged.Fields[name], want) {
			t.Fatalf("field %s changed", name)
		}
	}
	for sym := range merged.Deps {
		merged.Deps[sym] = regionDependencyState{Valid: false}
	}
	for _, input := range []regionRefState{left, right} {
		for _, dep := range input.Deps {
			if !dep.Valid {
				t.Fatal("merged outer dependency aliases an input")
			}
		}
	}
}

func TestReusableRegionFieldRequiresNormalizedLeaf(t *testing.T) {
	base := regionRefStateFromDependency(&Symbol{Name: "backing"}, 1)
	cases := map[string]func(*regionRefState){
		"unknown summary":              func(s *regionRefState) { s.PackedStoreSummaryKnown = false },
		"empty fields":                 func(s *regionRefState) { s.Fields = map[string]regionRefState{} },
		"empty store deps":             func(s *regionRefState) { s.StoreDeps = map[*Symbol]packedStoreDependencyState{} },
		"empty parameter words":        func(s *regionRefState) { s.ParamDeps.extra = []uint64{} },
		"duplicate parameter":          func(s *regionRefState) { appendRegionParamDep(s, 3); s.ParamDeps.Add(3) },
		"missing direct parameter":     func(s *regionRefState) { s.ParamDeps.Add(3) },
		"negative direct parameter":    func(s *regionRefState) { s.HasDirectParamDep = true; s.DirectParamDep = -1 },
		"trailing zero parameter word": func(s *regionRefState) { appendRegionParamDep(s, 3); s.ParamDeps.extra = []uint64{1, 0} },
		"cycle":                        func(s *regionRefState) { s.Fields = map[string]regionRefState{}; s.Fields["self"] = *s },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			state := base
			change(&state)
			if sameReusableRegionRefField(state, state) {
				t.Fatal("accepted a leaf requiring normalization or recursive merging")
			}
		})
	}
}

func BenchmarkMergeSupersetRegionFields(b *testing.B) {
	left, right, _ := sharedFieldMergeFixture(64)
	left.Fields = map[string]regionRefState{"field_0": right.Fields["field_0"]}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		merged, ok := mergeRegionRefStates(left, right)
		if !ok || len(merged.Fields) != 64 {
			b.Fatal("lost fields")
		}
	}
}

func TestMergeSupersetRegionFieldsPreservesOverlaps(t *testing.T) {
	left, right, full := sharedFieldMergeFixture(8)
	left.Fields = map[string]regionRefState{"field_0": full["field_0"]}
	for _, states := range [][]regionRefState{{left, right}, {right, left}} {
		merged, ok := mergeRegionRefStates(states...)
		if !ok || len(merged.Deps) != 2 || regionRefFieldsIdentity(merged.Fields) != regionRefFieldsIdentity(full) {
			t.Fatal("compatible superset did not preserve its immutable fields")
		}
	}
	// Equal field keys alone do not permit reuse: differing region dependencies
	// must still be unioned instead of discarding the smaller input's value.
	other := regionRefStateFromDependency(&Symbol{Name: "other"}, 3)
	left.Fields = map[string]regionRefState{"field_0": other}
	want, _ := mergeFlatRegionRefStates(other, full["field_0"])
	merged, ok := mergeRegionRefStates(left, right)
	if !ok || !reflect.DeepEqual(merged.Fields["field_0"], want) || regionRefFieldsIdentity(merged.Fields) == regionRefFieldsIdentity(full) {
		t.Fatal("incompatible overlap was reused instead of merged")
	}
	if len(full["field_0"].Deps) != 1 {
		t.Fatal("merging changed an input field")
	}
	left.Fields = map[string]regionRefState{"extra": other}
	merged, ok = mergeRegionRefStates(left, right)
	if !ok || len(merged.Fields) != len(full)+1 || !reflect.DeepEqual(merged.Fields["extra"], other) {
		t.Fatal("disjoint inputs lost a field or its provenance")
	}
	for name, field := range full {
		if !reflect.DeepEqual(merged.Fields[name], field) {
			t.Fatalf("disjoint merge changed %s", name)
		}
	}
	delete(merged.Fields, "field_0")
	if len(full) != 8 {
		t.Fatal("disjoint merge aliases an input map")
	}
}

func BenchmarkMergeDisjointRegionFields(b *testing.B) {
	left, right, _ := sharedFieldMergeFixture(64)
	left.Fields = map[string]regionRefState{"extra": right.Fields["field_0"]}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		merged, ok := mergeRegionRefStates(left, right)
		if !ok || len(merged.Fields) != 65 {
			b.Fatal("lost fields")
		}
	}
}

func BenchmarkMergeEqualRegionFieldDependencies(b *testing.B) {
	left, right, _ := sharedFieldMergeFixture(64)
	right.Fields = cloneRegionRefFields(right.Fields)
	for name, leaf := range right.Fields {
		right.Fields[name] = cloneRegionRefState(leaf)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		merged, ok := mergeRegionRefStates(left, right)
		if !ok || len(merged.Fields) != 64 {
			b.Fatal("lost fields")
		}
	}
}

func TestMergeEqualRegionFieldDependenciesPreservesInvalidation(t *testing.T) {
	left, right, fields := sharedFieldMergeFixture(8)
	right.Fields = cloneRegionRefFields(fields)
	for name, leaf := range right.Fields {
		right.Fields[name] = cloneRegionRefState(leaf)
	}
	merged, ok := mergeRegionRefStates(left, right)
	if !ok || regionRefFieldsIdentity(merged.Fields) != regionRefFieldsIdentity(fields) {
		t.Fatal("equal separately allocated dependency maps were copied")
	}
	leaf := right.Fields["field_0"]
	for sym, dep := range leaf.Deps {
		dep.Valid = false
		dep.Generation++
		dep.InvalidatedBy = "callee growth"
		leaf.Deps[sym] = dep
	}
	// Replace the changed field in a private map, as all flow edits must do.
	right.Fields = cloneRegionRefFields(right.Fields)
	right.Fields["field_0"] = leaf
	want, _ := mergeFlatRegionRefStates(fields["field_0"], leaf)
	merged, ok = mergeRegionRefStates(left, right)
	if !ok || !reflect.DeepEqual(merged.Fields["field_0"], want) {
		t.Fatal("field merge lost dependency invalidation metadata")
	}
	for _, dep := range fields["field_0"].Deps {
		if !dep.Valid || dep.InvalidatedBy != "" {
			t.Fatal("invalidation changed the other branch")
		}
	}
}

func TestMergeSupersetKeepsFirstParameterFieldRepresentation(t *testing.T) {
	changes := map[string]func(*regionRefState){
		"empty fields":             func(s *regionRefState) { s.Fields = map[string]regionRefState{} },
		"empty dependencies":       func(s *regionRefState) { s.Deps = map[*Symbol]regionDependencyState{} },
		"empty store dependencies": func(s *regionRefState) { s.StoreDeps = map[*Symbol]packedStoreDependencyState{} },
		"empty parameter words":    func(s *regionRefState) { s.ParamDeps.extra = []uint64{} },
		"zero parameter words":     func(s *regionRefState) { s.ParamDeps.extra = []uint64{0} },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			later := regionRefState{ParamOnlySummary: true, HasDirectParamDep: true, DirectParamDep: 1}
			first := later
			change(&first)
			left := regionRefState{Fields: map[string]regionRefState{"shared": first}}
			right := regionRefState{Fields: map[string]regionRefState{"shared": later, "extra": later}}
			want, _ := mergeFlatRegionRefStates(first, later)
			merged, ok := mergeRegionRefStates(left, right)
			if !ok || !reflect.DeepEqual(merged.Fields["shared"], want) {
				t.Fatal("superset reuse changed the first parameter field representation")
			}
		})
	}
}

func BenchmarkMergeMixedRegionParameterFields(b *testing.B) {
	left, right, _ := sharedFieldMergeFixture(64)
	for name, leaf := range left.Fields {
		appendRegionParamDep(&leaf, 3)
		appendRegionParamDep(&leaf, 70)
		left.Fields[name] = leaf
	}
	right.Fields = cloneRegionRefFields(left.Fields)
	for name, leaf := range right.Fields {
		right.Fields[name] = cloneRegionRefState(leaf)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		merged, ok := mergeRegionRefStates(left, right)
		if !ok || len(merged.Fields) != 64 {
			b.Fatal("lost fields")
		}
	}
}

func TestMergeMixedRegionParameterFieldsMatchesFlatMerge(t *testing.T) {
	for _, indices := range [][]int{{0}, {63, 1}, {70, 3, 128}, {3, 70}} {
		t.Run(fmt.Sprint(indices), func(t *testing.T) {
			left, right, _ := sharedFieldMergeFixture(4)
			for name, leaf := range left.Fields {
				for _, index := range indices {
					appendRegionParamDep(&leaf, index)
				}
				left.Fields[name] = leaf
			}
			right.Fields = cloneRegionRefFields(left.Fields)
			for name, leaf := range right.Fields {
				right.Fields[name] = cloneRegionRefState(leaf)
			}
			merged, ok := mergeRegionRefStates(left, right)
			if !ok || regionRefFieldsIdentity(merged.Fields) != regionRefFieldsIdentity(left.Fields) {
				t.Fatal("normalized mixed provenance was copied")
			}
			for name, leaf := range left.Fields {
				want, valid := mergeFlatRegionRefStates(leaf, right.Fields[name])
				if !valid || !reflect.DeepEqual(merged.Fields[name], want) {
					t.Fatal("mixed parameter or region provenance changed")
				}
			}
		})
	}
}
