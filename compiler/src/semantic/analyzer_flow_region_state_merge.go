package semantic

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"elisacore/src/ast"
)

func mergeRegionRefStates(states ...regionRefState) (regionRefState, bool) {
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
			if !ok || !sameReusableRegionRefField(prior, next) {
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
					if next, ok := mergeFlatRegionRefStates(existing, fieldState); ok {
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
func sameReusableRegionRefField(left, right regionRefState) bool {
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

// A flat merge replays parameter dependencies through appendRegionParamDep.
// Only reuse leaves whose direct slot and bitset already have that representation.
func canonicalRegionRefParamDeps(state regionRefState) bool {
	if !state.HasDirectParamDep {
		return state.DirectParamDep == 0 && state.ParamDeps.inline == 0 && state.ParamDeps.extra == nil
	}
	if state.DirectParamDep < 0 || state.ParamDeps.Contains(state.DirectParamDep) {
		return false
	}
	if state.ParamDeps.extra != nil {
		return len(state.ParamDeps.extra) != 0 && state.ParamDeps.extra[len(state.ParamDeps.extra)-1] != 0
	}
	return true
}

func shareIdenticalRegionRefStates(states []regionRefState) (regionRefState, bool) {
	if len(states) < 2 || !hasRegionProvenance(states[0]) {
		return regionRefState{}, false
	}
	base := states[0]
	depsID := regionDependencyMapIdentity(base.Deps)
	storeDepsID := packedStoreDependencyMapIdentity(base.StoreDeps)
	fieldsID := regionRefFieldsIdentity(base.Fields)
	for _, state := range states[1:] {
		if state.DirectParamDep != base.DirectParamDep || state.HasDirectParamDep != base.HasDirectParamDep ||
			!sameRegionParamDeps(state.ParamDeps, base.ParamDeps) ||
			regionDependencyMapIdentity(state.Deps) != depsID ||
			packedStoreDependencyMapIdentity(state.StoreDeps) != storeDepsID ||
			regionRefFieldsIdentity(state.Fields) != fieldsID ||
			state.PackedStoreSummaryKnown != base.PackedStoreSummaryKnown ||
			(state.PackedStoreSummaryKnown && state.PackedStoreSummary != base.PackedStoreSummary) ||
			state.ParamOnlySummary != base.ParamOnlySummary {
			return regionRefState{}, false
		}
	}
	merged := cloneRegionRefStateSharedFields(base)
	// Identity helpers equate nil and empty maps. Normalize the result just
	// like the ordinary merge; summary convergence observes representation.
	merged.Fields = shareRegionRefFields(merged.Fields)
	if len(merged.Deps) == 0 {
		merged.Deps = nil
	}
	if len(merged.StoreDeps) == 0 {
		merged.StoreDeps = nil
	}
	merged.DirectParamDep, merged.HasDirectParamDep, merged.ParamDeps = 0, false, IntBitSet{}
	forEachRegionParamDep(base, func(index int) { appendRegionParamDep(&merged, index) })
	merged.PackedStoreSummary = summarizePackedStoreProvenance(base)
	merged.PackedStoreSummaryKnown = true
	return merged, true
}

func regionDependencyMapIdentity(deps map[*Symbol]regionDependencyState) uintptr {
	if len(deps) == 0 {
		return 0
	}
	return reflect.ValueOf(deps).Pointer()
}

func packedStoreDependencyMapIdentity(deps map[*Symbol]packedStoreDependencyState) uintptr {
	if len(deps) == 0 {
		return 0
	}
	return reflect.ValueOf(deps).Pointer()
}

func mergeFlatRegionRefStates(left regionRefState, right regionRefState) (regionRefState, bool) {
	if sameParamOnlyRegionRefSummary(left, right) {
		return cloneRegionRefStateSharedFields(left), true
	}
	if len(left.Fields) != 0 || len(right.Fields) != 0 {
		return mergeRegionRefStates(left, right)
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

func mergeRegionRefStatesWithExplicitFields(states []regionRefState, fieldStates map[string]regionRefState) (regionRefState, bool) {
	merged := regionRefState{PackedStoreSummaryKnown: true, ParamOnlySummary: true}
	found := false
	for _, state := range states {
		if !hasRegionProvenance(state) {
			continue
		}
		found = true
		merged.ParamOnlySummary = merged.ParamOnlySummary && state.ParamOnlySummary
		mergePackedStoreProvenanceInto(&merged.PackedStoreSummary, summarizePackedStoreProvenance(state))
		if len(state.Deps) != 0 {
			if merged.Deps == nil {
				merged.Deps = map[*Symbol]regionDependencyState{}
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
				merged.StoreDeps = map[*Symbol]packedStoreDependencyState{}
			}
			for store, dep := range state.StoreDeps {
				merged.StoreDeps[store] = dep
			}
		}
		forEachRegionParamDep(state, func(index int) {
			appendRegionParamDep(&merged, index)
		})
	}
	if len(fieldStates) != 0 {
		for _, fieldState := range fieldStates {
			merged.ParamOnlySummary = merged.ParamOnlySummary && fieldState.ParamOnlySummary
		}
		merged.Fields = fieldStates
		found = true
		merged.PackedStoreSummaryKnown = false
		return withPackedStoreProvenanceSummary(merged), true
	}
	if !found || !hasRegionProvenance(merged) {
		return regionRefState{}, false
	}
	return merged, true
}

func regionIndexFieldKey(index int64) string {
	return fmt.Sprintf("[%d]", index)
}

func regionAnyIndexFieldKey() string {
	return "[*]"
}

func isRegionIndexFieldKey(name string) bool {
	return strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]")
}

func projectRegionFieldState(state regionRefState, field string) (regionRefState, bool) {
	if len(state.Fields) == 0 {
		return regionRefState{}, false
	}
	fieldState, ok := state.Fields[field]
	if !ok || !hasRegionProvenance(fieldState) {
		return regionRefState{}, false
	}
	return cloneRegionRefState(fieldState), true
}

func projectRegionIndexState(state regionRefState, index ast.Expr, evalConst func(ast.Expr) (ConstValue, bool)) (regionRefState, bool) {
	if len(state.Fields) == 0 {
		return regionRefState{}, false
	}
	if evalConst != nil {
		if value, ok := evalConst(index); ok && value.Kind == ConstInt {
			if fieldState, ok := projectRegionIndexKeyState(state, regionIndexFieldKey(value.Int)); ok {
				return fieldState, true
			}
		}
	}
	return projectRegionIndexKeyState(state, regionAnyIndexFieldKey())
}

func projectRegionIndexKeyState(state regionRefState, key string) (regionRefState, bool) {
	if len(state.Fields) == 0 {
		return regionRefState{}, false
	}
	fieldState, ok := state.Fields[key]
	if !ok || !hasRegionProvenance(fieldState) {
		return regionRefState{}, false
	}
	return cloneRegionRefState(fieldState), true
}

func summarizeRegionIndexStates(state regionRefState) (regionRefState, bool) {
	if !hasRegionProvenance(state) {
		return regionRefState{}, false
	}
	summary := cloneRegionRefStateShallowFields(state)
	if len(state.Fields) == 0 {
		return summary, true
	}
	indexStates := make([]regionRefState, 0, len(state.Fields))
	for _, name := range slices.Sorted(maps.Keys(state.Fields)) { // map: sort — the merge below is order-sensitive
		fieldState := state.Fields[name]
		if !isRegionIndexFieldKey(name) {
			continue
		}
		if !hasRegionProvenance(fieldState) {
			continue
		}
		indexStates = append(indexStates, fieldState)
	}
	if len(indexStates) == 0 {
		summary.Fields = nil
		return withPackedStoreProvenanceSummary(summary), true
	}
	merged, ok := mergeRegionRefStates(indexStates...)
	if !ok {
		summary.Fields = nil
		return withPackedStoreProvenanceSummary(summary), true
	}
	summary.Fields = map[string]regionRefState{
		regionAnyIndexFieldKey(): merged,
	}
	summary.PackedStoreSummaryKnown = false
	return withPackedStoreProvenanceSummary(summary), true
}
