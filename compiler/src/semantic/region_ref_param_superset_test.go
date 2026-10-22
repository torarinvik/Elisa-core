package semantic

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"
)

func referenceMergeRegionRefStates(states ...regionRefState) (regionRefState, bool) {
	if len(states) == 1 {
		state := states[0]
		if !hasRegionProvenance(state) {
			return regionRefState{}, false
		}
		// There is nothing to merge across fields, so preserve the immutable Fields
		// map. Clone the mutable dependency maps, and normalize parameter dependencies
		// exactly as the multi-state path does.
		merged := cloneRegionRefState(state)
		merged.DirectParamDep, merged.HasDirectParamDep, merged.ParamDeps = 0, false, IntBitSet{}
		forEachRegionParamDep(state, func(index int) {
			appendRegionParamDep(&merged, index)
		})
		merged.PackedStoreSummary = summarizePackedStoreProvenance(state)
		merged.PackedStoreSummaryKnown = true
		for _, fieldState := range merged.Fields {
			if fieldState.Fields != nil && len(fieldState.Fields) == 0 {
				merged.Fields = cloneRegionRefFields(merged.Fields)
				break
			}
		}
		return merged, true
	}
	if merged, ok := shareIdenticalRegionRefStates(states); ok {
		return merged, true
	}
	// A later input may already contain the union of all field keys. Choose the
	// largest map, then reuse it only if every overlap merges to that same value.
	fieldSource := -1
	for i, state := range states {
		if len(state.Fields) != 0 && (fieldSource < 0 || len(state.Fields) > len(states[fieldSource].Fields)) {
			fieldSource = i
		}
	}
	shareFieldSource := true
	for i, state := range states {
		if len(state.Fields) == 0 || i == fieldSource {
			continue
		}
		base := states[fieldSource].Fields
		for name, next := range state.Fields {
			prior, ok := base[name]
			if !ok || !referenceSameReusableRegionRefField(prior, next) {
				shareFieldSource = false
				break
			}
		}
	}
	merged := regionRefState{PackedStoreSummaryKnown: true, ParamOnlySummary: true}
	for i, state := range states {
		if !hasRegionProvenance(state) {
			continue
		}
		merged.ParamOnlySummary = merged.ParamOnlySummary && state.ParamOnlySummary
		mergePackedStoreProvenanceInto(&merged.PackedStoreSummary, summarizePackedStoreProvenance(state))
		if len(state.Deps) != 0 {
			if merged.Deps == nil {
				merged.Deps = make(map[*Symbol]regionDependencyState, len(state.Deps))
			}
			for region, dep := range state.Deps {
				existing, ok := merged.Deps[region]
				if !ok {
					merged.Deps[region] = dep
					continue
				}
				if !existing.Valid {
					if !dep.Valid && dep.Generation > existing.Generation {
						merged.Deps[region] = dep
					}
					continue
				}
				if !dep.Valid {
					merged.Deps[region] = dep
					continue
				}
				if dep.Generation > existing.Generation {
					merged.Deps[region] = dep
				}
			}
		}
		if len(state.StoreDeps) != 0 {
			if merged.StoreDeps == nil {
				merged.StoreDeps = make(map[*Symbol]packedStoreDependencyState, len(state.StoreDeps))
			}
			for store, dep := range state.StoreDeps {
				merged.StoreDeps[store] = dep
			}
		}
		forEachRegionParamDep(state, func(index int) {
			appendRegionParamDep(&merged, index)
		})
		if len(state.Fields) != 0 {
			if shareFieldSource {
				if i != fieldSource {
					continue
				}
				merged.Fields = shareRegionRefFields(state.Fields)
				for _, fieldState := range merged.Fields {
					if fieldState.Fields != nil && len(fieldState.Fields) == 0 {
						merged.Fields = cloneRegionRefFields(merged.Fields)
						break
					}
				}
				continue
			}
			if merged.Fields == nil {
				// The union contains at least as many keys as its largest input.
				// Starting with a small earlier map otherwise grows and copies it
				// repeatedly while the later input is inserted.
				merged.Fields = make(map[string]regionRefState, len(states[fieldSource].Fields))
			}
			for name, fieldState := range state.Fields {
				if existing, ok := merged.Fields[name]; ok {
					if next, ok := referenceMergeFlatRegionRefStates(existing, fieldState); ok {
						merged.Fields[name] = next
					} else {
						delete(merged.Fields, name)
					}
				} else {
					merged.Fields[name] = cloneRegionRefStateSharedFields(fieldState)
				}
			}
		}
	}
	if !hasRegionProvenance(merged) {
		return regionRefState{}, false
	}
	return merged, true
}

// Reuse real-region leaves only when flat merging would preserve every value.
// Nested fields retain the existing recursive merge and cycle boundaries.
func referenceSameReusableRegionRefField(left, right regionRefState) bool {
	if sameParamOnlyRegionRefSummary(left, right) {
		// Flat parameter-only merging keeps the first value verbatim. A
		// superset chosen from a later input must preserve even empty-map and
		// slice representations used by reflect.DeepEqual convergence checks.
		return (left.Fields == nil) == (right.Fields == nil) &&
			(left.Deps == nil) == (right.Deps == nil) && maps.Equal(left.Deps, right.Deps) &&
			(left.StoreDeps == nil) == (right.StoreDeps == nil) && maps.Equal(left.StoreDeps, right.StoreDeps) &&
			(left.ParamDeps.extra == nil) == (right.ParamDeps.extra == nil) && slices.Equal(left.ParamDeps.extra, right.ParamDeps.extra) &&
			left.PackedStoreSummary == right.PackedStoreSummary
	}
	for _, state := range []regionRefState{left, right} {
		if state.Fields != nil || !hasRegionProvenance(state) ||
			!state.PackedStoreSummaryKnown || !canonicalRegionRefParamDeps(state) ||
			(state.Deps != nil && len(state.Deps) == 0) ||
			(state.StoreDeps != nil && len(state.StoreDeps) == 0) {
			return false
		}
	}
	return left.ParamOnlySummary == right.ParamOnlySummary &&
		left.HasDirectParamDep == right.HasDirectParamDep && left.DirectParamDep == right.DirectParamDep &&
		sameRegionParamDeps(left.ParamDeps, right.ParamDeps) &&
		left.PackedStoreSummary == right.PackedStoreSummary &&
		(regionDependencyMapIdentity(left.Deps) == regionDependencyMapIdentity(right.Deps) || maps.Equal(left.Deps, right.Deps)) &&
		(packedStoreDependencyMapIdentity(left.StoreDeps) == packedStoreDependencyMapIdentity(right.StoreDeps) || maps.Equal(left.StoreDeps, right.StoreDeps))
}

func referenceMergeFlatRegionRefStates(left regionRefState, right regionRefState) (regionRefState, bool) {
	if sameParamOnlyRegionRefSummary(left, right) {
		return cloneRegionRefStateSharedFields(left), true
	}
	if len(left.Fields) != 0 || len(right.Fields) != 0 {
		return referenceMergeRegionRefStates(left, right)
	}
	if !hasRegionProvenance(left) {
		if !hasRegionProvenance(right) {
			return regionRefState{}, false
		}
		return cloneRegionRefStateSharedFields(right), true
	}
	if !hasRegionProvenance(right) {
		return cloneRegionRefStateSharedFields(left), true
	}

	merged := regionRefState{PackedStoreSummaryKnown: true, ParamOnlySummary: left.ParamOnlySummary && right.ParamOnlySummary}
	mergePackedStoreProvenanceInto(&merged.PackedStoreSummary, summarizePackedStoreProvenance(left))
	mergePackedStoreProvenanceInto(&merged.PackedStoreSummary, summarizePackedStoreProvenance(right))

	if len(left.Deps) != 0 || len(right.Deps) != 0 {
		merged.Deps = make(map[*Symbol]regionDependencyState, len(left.Deps)+len(right.Deps))
		for region, dep := range left.Deps {
			merged.Deps[region] = dep
		}
		for region, dep := range right.Deps {
			existing, ok := merged.Deps[region]
			if !ok {
				merged.Deps[region] = dep
				continue
			}
			if !existing.Valid {
				if !dep.Valid && dep.Generation > existing.Generation {
					merged.Deps[region] = dep
				}
				continue
			}
			if !dep.Valid {
				merged.Deps[region] = dep
				continue
			}
			if dep.Generation > existing.Generation {
				merged.Deps[region] = dep
			}
		}
	}
	if len(left.StoreDeps) != 0 || len(right.StoreDeps) != 0 {
		merged.StoreDeps = make(map[*Symbol]packedStoreDependencyState, len(left.StoreDeps)+len(right.StoreDeps))
		for store, dep := range left.StoreDeps {
			merged.StoreDeps[store] = dep
		}
		for store, dep := range right.StoreDeps {
			merged.StoreDeps[store] = dep
		}
	}
	forEachRegionParamDep(left, func(index int) {
		appendRegionParamDep(&merged, index)
	})
	forEachRegionParamDep(right, func(index int) {
		appendRegionParamDep(&merged, index)
	})
	return merged, true
}

func paramSupersetMergeFixture(width int) (regionRefState, regionRefState) {
	child := regionRefStateFromParamDependency(9)
	nested := map[string]regionRefState{"value": child, "other": child, "third": child}
	leftLeaf := child
	leftLeaf.Fields = nested
	appendRegionParamDep(&leftLeaf, 1)
	rightLeaf := child
	rightLeaf.Fields = nested
	left := regionRefState{Fields: map[string]regionRefState{}}
	right := regionRefState{Fields: map[string]regionRefState{}}
	for i := 0; i < width; i++ {
		name := fmt.Sprint(i)
		left.Fields[name] = leftLeaf
		right.Fields[name] = rightLeaf
	}
	return left, right
}

func TestParamFieldSupersetMergeMatchesPriorNormalization(t *testing.T) {
	for _, index := range []int{1, 63, 64, 70, 128} {
		for _, reverse := range []bool{false, true} {
			left, right := paramSupersetMergeFixture(8)
			for name, leaf := range left.Fields {
				leaf.ParamDeps = IntBitSet{}
				appendRegionParamDep(&leaf, index)
				left.Fields[name] = leaf
			}
			if reverse {
				left, right = right, left
			}
			want, wok := referenceMergeRegionRefStates(left, right)
			got, gok := mergeRegionRefStates(left, right)
			if gok != wok || !reflect.DeepEqual(got, want) {
				t.Fatalf("changed provenance for index %d, reversed %v", index, reverse)
			}
		}
	}
	left, right := paramSupersetMergeFixture(8)
	got, ok := mergeRegionRefStates(left, right)
	if !ok || regionRefFieldsIdentity(got.Fields) != regionRefFieldsIdentity(left.Fields) {
		t.Fatal("subsumed dependency still copied field map")
	}
	// Unknown summaries, noncanonical representations, and different nested fields
	// must retain the prior normalization and merging behavior.
	for _, change := range []func(*regionRefState){
		func(s *regionRefState) { s.PackedStoreSummaryKnown = false },
		func(s *regionRefState) { s.ParamDeps.extra = []uint64{0} },
		func(s *regionRefState) { s.Fields = map[string]regionRefState{} },
		func(s *regionRefState) { s.Deps = map[*Symbol]regionDependencyState{} },
		func(s *regionRefState) { s.DirectParamDep = 3 },
		func(s *regionRefState) {
			s.Fields = map[string]regionRefState{"different": regionRefStateFromParamDependency(70)}
		},
	} {
		left, right := paramSupersetMergeFixture(4)
		for name, leaf := range left.Fields {
			change(&leaf)
			left.Fields[name] = leaf
		}
		want, wok := referenceMergeRegionRefStates(left, right)
		got, gok := mergeRegionRefStates(left, right)
		if gok != wok || !reflect.DeepEqual(got, want) {
			t.Fatal("changed normalization of non-reusable field")
		}
	}
}

func BenchmarkParamFieldSupersetMerge(b *testing.B) {
	left, right := paramSupersetMergeFixture(227)
	for _, tc := range []struct {
		name string
		run  func(...regionRefState) (regionRefState, bool)
	}{{"prior", referenceMergeRegionRefStates}, {"reuse", mergeRegionRefStates}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				got, ok := tc.run(left, right)
				if !ok || len(got.Fields) != 227 {
					b.Fatal("lost provenance")
				}
			}
		})
	}
}
