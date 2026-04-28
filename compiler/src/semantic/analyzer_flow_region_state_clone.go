package semantic

func (a *Analyzer) cloneRegionStates() map[*Symbol]regionState {
	if a.currentRegions == nil {
		return nil
	}
	cloned := make(map[*Symbol]regionState, len(a.currentRegions))
	for sym, state := range a.currentRegions {
		cloned[sym] = state
	}
	return cloned
}

func (a *Analyzer) cloneRegionMarkStates() map[*Symbol]regionMarkState {
	if a.currentRegionMarks == nil {
		return nil
	}
	cloned := make(map[*Symbol]regionMarkState, len(a.currentRegionMarks))
	for sym, state := range a.currentRegionMarks {
		cloned[sym] = state
	}
	return cloned
}

func (a *Analyzer) cloneCheckpointStates() map[*Symbol]checkpointState {
	if a.currentCheckpoints == nil {
		return nil
	}
	cloned := make(map[*Symbol]checkpointState, len(a.currentCheckpoints))
	for sym, state := range a.currentCheckpoints {
		cloned[sym] = state
	}
	return cloned
}

func (a *Analyzer) cloneRegionRefStates() map[*Symbol]regionRefState {
	if a.currentRegionRefs == nil {
		return nil
	}
	cloned := make(map[*Symbol]regionRefState, len(a.currentRegionRefs))
	for sym, state := range a.currentRegionRefs {
		cloned[sym] = cloneRegionRefStateSharedFields(state)
	}
	return cloned
}

func cloneRegionDependencyStates(src map[*Symbol]regionDependencyState) map[*Symbol]regionDependencyState {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[*Symbol]regionDependencyState, len(src))
	for region, dep := range src {
		cloned[region] = dep
	}
	return cloned
}

func clonePackedStoreDependencyStates(src map[*Symbol]packedStoreDependencyState) map[*Symbol]packedStoreDependencyState {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[*Symbol]packedStoreDependencyState, len(src))
	for store, dep := range src {
		cloned[store] = dep
	}
	return cloned
}

func cloneRegionParamDeps(src IntBitSet) IntBitSet {
	return src.Clone()
}

func hasRegionParamDependencies(state regionRefState) bool {
	return state.HasDirectParamDep || !state.ParamDeps.IsEmpty()
}

func regionRefStateHasParamDep(state regionRefState, index int) bool {
	if state.HasDirectParamDep && state.DirectParamDep == index {
		return true
	}
	return state.ParamDeps.Contains(index)
}

func sameRegionParamDeps(left IntBitSet, right IntBitSet) bool {
	if left.inline != right.inline {
		return false
	}
	if len(left.extra) != len(right.extra) {
		return false
	}
	for i, word := range left.extra {
		if right.extra[i] != word {
			return false
		}
	}
	return true
}

func sameParamOnlyRegionRefSummary(left regionRefState, right regionRefState) bool {
	if !left.ParamOnlySummary || !right.ParamOnlySummary {
		return false
	}
	if left.DirectParamDep != right.DirectParamDep || left.HasDirectParamDep != right.HasDirectParamDep {
		return false
	}
	if !sameRegionParamDeps(left.ParamDeps, right.ParamDeps) {
		return false
	}
	if regionRefFieldsIdentity(left.Fields) != regionRefFieldsIdentity(right.Fields) {
		return false
	}
	if left.PackedStoreSummaryKnown != right.PackedStoreSummaryKnown {
		return false
	}
	if left.PackedStoreSummaryKnown && left.PackedStoreSummary != right.PackedStoreSummary {
		return false
	}
	return true
}

func regionRefStateParamDepCount(state regionRefState) int {
	if !state.HasDirectParamDep {
		return state.ParamDeps.Count()
	}
	if !state.ParamDeps.Contains(state.DirectParamDep) {
		return state.ParamDeps.Count() + 1
	}
	return state.ParamDeps.Count()
}

func singleRegionParamDepIndex(state regionRefState) (int, bool) {
	if regionRefStateParamDepCount(state) != 1 {
		return 0, false
	}
	if state.HasDirectParamDep {
		return state.DirectParamDep, true
	}
	index := 0
	state.ParamDeps.ForEach(func(value int) {
		index = value
	})
	return index, true
}

func forEachRegionParamDep(state regionRefState, fn func(int)) {
	if fn == nil {
		return
	}
	if state.HasDirectParamDep {
		fn(state.DirectParamDep)
	}
	state.ParamDeps.ForEach(func(index int) {
		if state.HasDirectParamDep && index == state.DirectParamDep {
			return
		}
		fn(index)
	})
}

func appendRegionParamDep(state *regionRefState, index int) {
	if state == nil || index < 0 {
		return
	}
	if state.HasDirectParamDep {
		if state.DirectParamDep == index {
			return
		}
		state.ParamDeps.Add(index)
		return
	}
	if !state.ParamDeps.IsEmpty() {
		state.ParamDeps.Add(index)
		return
	}
	state.DirectParamDep = index
	state.HasDirectParamDep = true
}

func cloneRegionRefFields(src map[string]regionRefState) map[string]regionRefState {
	if len(src) == 0 {
		return nil
	}
	cloned := make(map[string]regionRefState, len(src))
	for name, fieldState := range src {
		cloned[name] = fieldState
	}
	return cloned
}

func cloneRegionRefState(state regionRefState) regionRefState {
	return regionRefState{
		Deps:                    cloneRegionDependencyStates(state.Deps),
		StoreDeps:               clonePackedStoreDependencyStates(state.StoreDeps),
		DirectParamDep:          state.DirectParamDep,
		HasDirectParamDep:       state.HasDirectParamDep,
		ParamDeps:               cloneRegionParamDeps(state.ParamDeps),
		Fields:                  cloneRegionRefFields(state.Fields),
		PackedStoreSummary:      state.PackedStoreSummary,
		PackedStoreSummaryKnown: state.PackedStoreSummaryKnown,
		ParamOnlySummary:        state.ParamOnlySummary,
	}
}

func cloneRegionRefStateSharedFields(state regionRefState) regionRefState {
	return regionRefState{
		Deps:                    state.Deps,
		StoreDeps:               state.StoreDeps,
		DirectParamDep:          state.DirectParamDep,
		HasDirectParamDep:       state.HasDirectParamDep,
		ParamDeps:               state.ParamDeps,
		Fields:                  state.Fields,
		PackedStoreSummary:      state.PackedStoreSummary,
		PackedStoreSummaryKnown: state.PackedStoreSummaryKnown,
		ParamOnlySummary:        state.ParamOnlySummary,
	}
}

func cloneRegionRefStateShallowFields(state regionRefState) regionRefState {
	return withPackedStoreProvenanceSummary(regionRefState{
		Deps:              state.Deps,
		StoreDeps:         state.StoreDeps,
		DirectParamDep:    state.DirectParamDep,
		HasDirectParamDep: state.HasDirectParamDep,
		ParamDeps:         state.ParamDeps,
		ParamOnlySummary:  len(state.Deps) == 0 && len(state.StoreDeps) == 0,
	})
}

func withPackedStoreProvenanceSummary(state regionRefState) regionRefState {
	if state.PackedStoreSummaryKnown {
		return state
	}
	state.PackedStoreSummary = summarizePackedStoreProvenance(state)
	state.PackedStoreSummaryKnown = true
	return state
}

func hasRegionDependencies(state regionRefState) bool {
	return len(state.Deps) != 0 || len(state.StoreDeps) != 0
}

func hasRegionProvenance(state regionRefState) bool {
	return hasRegionDependencies(state) || hasRegionParamDependencies(state) || len(state.Fields) != 0
}

func regionRefStateFromDependency(region *Symbol, generation int) regionRefState {
	if region == nil {
		return regionRefState{}
	}
	return withPackedStoreProvenanceSummary(regionRefState{
		Deps: map[*Symbol]regionDependencyState{
			region: {
				Generation: generation,
				Valid:      true,
			},
		},
	})
}

func regionRefStateFromPackedStoreDependency(store *Symbol, storeType *PackedEnumStoreType) regionRefState {
	if store == nil || storeType == nil {
		return regionRefState{}
	}
	return withPackedStoreProvenanceSummary(regionRefState{
		StoreDeps: map[*Symbol]packedStoreDependencyState{
			store: {Type: storeType},
		},
	})
}

func regionRefStateFromParamDependency(index int) regionRefState {
	if index < 0 {
		return regionRefState{}
	}
	return withPackedStoreProvenanceSummary(regionRefState{
		DirectParamDep:    index,
		HasDirectParamDep: true,
		ParamOnlySummary:  true,
	})
}
