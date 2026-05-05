package semantic

import (
	"fmt"
	"strings"

	"elisacore/src/ast"
)

func mergeRegionRefStates(states ...regionRefState) (regionRefState, bool) {
	merged := regionRefState{PackedStoreSummaryKnown: true, ParamOnlySummary: true}
	for _, state := range states {
		if !hasRegionProvenance(state) {
			continue
		}
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
		if len(state.Fields) != 0 {
			if merged.Fields == nil {
				merged.Fields = map[string]regionRefState{}
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
		merged.Deps = map[*Symbol]regionDependencyState{}
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
		merged.StoreDeps = map[*Symbol]packedStoreDependencyState{}
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
	for name, fieldState := range state.Fields {
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
