package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) clonePackedStores() map[string]*PackedEnumStoreType {
	if a.currentPackedStores == nil {
		return nil
	}
	cloned := make(map[string]*PackedEnumStoreType, len(a.currentPackedStores))
	for name, store := range a.currentPackedStores {
		cloned[name] = store
	}
	return cloned
}

func (a *Analyzer) clonePackedStoreResolutions() map[*Symbol]packedStoreResolution {
	if a.currentPackedStoreResolutions == nil {
		return nil
	}
	cloned := make(map[*Symbol]packedStoreResolution, len(a.currentPackedStoreResolutions))
	for sym, resolution := range a.currentPackedStoreResolutions {
		cloned[sym] = resolution
	}
	return cloned
}

func (a *Analyzer) bindActivePackedStoreType(t Type) {
	storeType, ok := t.(*PackedEnumStoreType)
	if !ok || storeType == nil || storeType.Enum == nil {
		return
	}
	if a.currentPackedStores == nil {
		a.currentPackedStores = map[string]*PackedEnumStoreType{}
	}
	a.currentPackedStores[storeType.Enum.Name] = storeType
}

func (a *Analyzer) lookupPackedStore(enumType *EnumType) (*PackedEnumStoreType, bool) {
	if a.currentPackedStores == nil || enumType == nil {
		return nil, false
	}
	store, ok := a.currentPackedStores[enumType.Name]
	if !ok || store == nil {
		return nil, false
	}
	return store, true
}

func (a *Analyzer) lookupRegionMark(name string) (*Symbol, regionMarkState) {
	if a.currentScope == nil {
		return nil, regionMarkState{}
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym.Kind != SymbolRegionMark {
		return nil, regionMarkState{}
	}
	state, ok := a.currentRegionMarks[sym]
	if !ok {
		return nil, regionMarkState{}
	}
	return sym, state
}

func (a *Analyzer) lookupCheckpoint(name string) (*Symbol, checkpointState) {
	if a.currentScope == nil {
		return nil, checkpointState{}
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym.Kind != SymbolCheckpoint {
		return nil, checkpointState{}
	}
	state, ok := a.currentCheckpoints[sym]
	if !ok {
		return nil, checkpointState{}
	}
	return sym, state
}

func (a *Analyzer) lookupRegionState(name string) (*Symbol, regionState) {
	if a.currentScope == nil {
		return nil, regionState{}
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok {
		return nil, regionState{}
	}
	// A region owner is anything registered in currentRegions: a `region` decl
	// (SymbolRegion) OR a local that received an owned region from a
	// `return move <region>` call (SymbolLocal — see markReceivedOwnedRegion).
	state, ok := a.currentRegions[sym]
	if !ok {
		return nil, regionState{}
	}
	return sym, state
}

func (a *Analyzer) recordRegionRefBinding(sym *Symbol, value ast.Expr) {
	if a.currentRegionRefs == nil || sym == nil {
		return
	}
	if storeType, ok := sym.Type.(*PackedEnumStoreType); ok {
		if state, ok := a.regionRefStateForExpr(value); ok && hasPackedStoreDependencies(state) {
			a.recordResolvedRegionRefBinding(sym, state)
			return
		}
		a.recordResolvedRegionRefBinding(sym, regionRefStateFromPackedStoreDependency(sym, storeType))
		return
	}
	state, ok := a.regionRefStateForExpr(value)
	if ok && hasRegionProvenance(state) {
		a.recordResolvedRegionRefBinding(sym, state)
		return
	}
	// Container `@r` provenance lives in the declared TYPE (DArrayType.Region etc.),
	// not the initializer expression (`= []` carries none). Derive the region
	// dependency from the type so a `darray[T] @r` binding is tracked exactly like a
	// `new[r]` borrow: invalidated when r is destroyed/reset, and recognized as
	// region-backed by promote.
	if cstate, cok := a.containerRegionDependency(sym.Type); cok {
		a.recordResolvedRegionRefBinding(sym, cstate)
		return
	}
	if ok {
		a.recordResolvedRegionRefBinding(sym, state)
		return
	}
	delete(a.currentRegionRefs, sym)
}

// containerRegionDependency derives a region dependency from a binding's declared
// container type (DArrayType/DictType/DStrType/SViewType all carry a `.Region`).
// Returns false for non-container types, region params not resolvable to a concrete
// live region, or an already-destroyed region.
func (a *Analyzer) containerRegionDependency(typ Type) (regionRefState, bool) {
	region := containerTypeRegion(typ)
	if region == "" {
		return regionRefState{}, false
	}
	sym, state := a.lookupRegionState(region)
	if sym == nil || state.Destroyed {
		return regionRefState{}, false
	}
	return regionRefStateFromDependency(sym, state.Generation), true
}

func containerTypeRegion(typ Type) string {
	switch t := StripAggregateStateType(stripRefForBounds(typ)).(type) {
	case *DArrayType:
		return t.Region
	case *DictType:
		return t.Region
	case *DStrType:
		return t.Region
	case *SViewType:
		return t.Region
	case *GenericInstanceType:
		return t.Region
	}
	return ""
}

// typeCanCarryRegion reports whether a type has a region field at all — i.e. whether
// a `@r` annotation on it is meaningful. References carry a region too, but they are
// resolved on a separate path; this gates the builtin/scalar case (`i32 @r` is rejected).
func typeCanCarryRegion(typ Type) bool {
	switch StripAggregateStateType(typ).(type) {
	case *DArrayType, *DictType, *DStrType, *SViewType, *GenericInstanceType, *RefType:
		return true
	}
	return false
}

func (a *Analyzer) freezeMovedPackedStoreSource(expr ast.Expr) (*Symbol, *PackedEnumStoreType, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	arg, ok := a.freezeStoreArg(call)
	if !ok {
		return nil, nil, false
	}
	moved, ok := explicitMoveOperand(arg)
	if !ok {
		return nil, nil, false
	}
	ident, ok := moved.(*ast.Ident)
	if !ok || a.currentScope == nil {
		return nil, nil, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return nil, nil, false
	}
	storeType, ok := sym.Type.(*PackedEnumStoreType)
	if !ok {
		return nil, nil, false
	}
	return sym, storeType, true
}

func (a *Analyzer) resolvePackedStoreDependency(store *Symbol, dep packedStoreDependencyState) (*Symbol, packedStoreDependencyState, bool) {
	if store == nil || len(a.currentPackedStoreResolutions) == 0 {
		return store, dep, false
	}
	resolvedStore := store
	resolvedDep := dep
	changed := false
	seen := map[*Symbol]bool{}
	for resolvedStore != nil {
		if seen[resolvedStore] {
			break
		}
		seen[resolvedStore] = true
		resolution, ok := a.currentPackedStoreResolutions[resolvedStore]
		if !ok {
			break
		}
		if resolution.Type != nil && resolution.Type != resolvedDep.Type {
			resolvedDep.Type = resolution.Type
			changed = true
		}
		if resolution.Symbol == nil || resolution.Symbol == resolvedStore {
			break
		}
		resolvedStore = resolution.Symbol
		changed = true
	}
	if changed && a.currentPackedStoreResolutions != nil {
		a.currentPackedStoreResolutions[store] = packedStoreResolution{Symbol: resolvedStore, Type: resolvedDep.Type}
	}
	return resolvedStore, resolvedDep, changed
}

func (a *Analyzer) resolvePackedStoreDependenciesInState(state regionRefState) (regionRefState, bool) {
	if len(a.currentPackedStoreResolutions) == 0 || !hasPackedStoreDependencies(state) {
		return state, false
	}
	changed := false
	storeDepsCloned := false
	fieldsCloned := false
	for store, dep := range state.StoreDeps {
		nextStore, nextDep, depChanged := a.resolvePackedStoreDependency(store, dep)
		if !depChanged {
			continue
		}
		if !storeDepsCloned {
			state.StoreDeps = clonePackedStoreDependencyStates(state.StoreDeps)
			storeDepsCloned = true
		}
		delete(state.StoreDeps, store)
		state.StoreDeps[nextStore] = nextDep
		changed = true
	}
	for name, fieldState := range state.Fields {
		if !hasPackedStoreDependencies(fieldState) {
			continue
		}
		nextField, fieldChanged := a.resolvePackedStoreDependenciesInState(fieldState)
		if !fieldChanged {
			continue
		}
		if !fieldsCloned {
			state.Fields = cloneRegionRefFields(state.Fields)
			fieldsCloned = true
		}
		state.Fields[name] = nextField
		changed = true
	}
	if changed {
		state.PackedStoreSummaryKnown = false
		state = withPackedStoreProvenanceSummary(state)
	}
	return state, changed
}

func (a *Analyzer) canonicalizeStoredRegionRefBinding(sym *Symbol, state regionRefState) regionRefState {
	nextState, changed := a.resolvePackedStoreDependenciesInState(state)
	if changed && a.currentRegionRefs != nil && sym != nil {
		a.currentRegionRefs[sym] = nextState
	}
	return nextState
}

func (a *Analyzer) recordResolvedRegionRefBinding(sym *Symbol, state regionRefState) {
	if a.currentRegionRefs == nil || sym == nil {
		return
	}
	if !hasRegionProvenance(state) {
		delete(a.currentRegionRefs, sym)
		return
	}
	state, _ = a.resolvePackedStoreDependenciesInState(state)
	state = withPackedStoreProvenanceSummary(state)
	a.currentRegionRefs[sym] = cloneRegionRefStateSharedFields(state)
}

func (a *Analyzer) recordRegionRefAssignment(target ast.Expr, value ast.Expr) {
	ident, ok := target.(*ast.Ident)
	if !ok || a.currentScope == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return
	}
	a.recordRegionRefBinding(sym, value)
}

func (a *Analyzer) remapPackedStoreDependencies(from *Symbol, to *Symbol, nextType *PackedEnumStoreType) {
	if from == nil || to == nil || nextType == nil {
		return
	}
	resolvedTo, resolvedDep, _ := a.resolvePackedStoreDependency(to, packedStoreDependencyState{Type: nextType})
	if a.currentPackedStoreResolutions == nil {
		a.currentPackedStoreResolutions = map[*Symbol]packedStoreResolution{}
	}
	a.currentPackedStoreResolutions[from] = packedStoreResolution{Symbol: resolvedTo, Type: resolvedDep.Type}
}

func remapPackedStoreDependencyInState(state regionRefState, from *Symbol, to *Symbol, nextType *PackedEnumStoreType) (regionRefState, bool) {
	changed := false
	storeDepsCloned := false
	fieldsCloned := false
	if dep, ok := state.StoreDeps[from]; ok {
		if !storeDepsCloned {
			state.StoreDeps = clonePackedStoreDependencyStates(state.StoreDeps)
			storeDepsCloned = true
		}
		delete(state.StoreDeps, from)
		dep.Type = nextType
		state.StoreDeps[to] = dep
		changed = true
	}
	for name, fieldState := range state.Fields {
		nextField, fieldChanged := remapPackedStoreDependencyInState(fieldState, from, to, nextType)
		if !fieldChanged {
			continue
		}
		if !fieldsCloned {
			state.Fields = cloneRegionRefFields(state.Fields)
			fieldsCloned = true
		}
		state.Fields[name] = nextField
		changed = true
	}
	if changed {
		state.PackedStoreSummaryKnown = false
		state = withPackedStoreProvenanceSummary(state)
	}
	return state, changed
}

func (a *Analyzer) invalidateRegionRefs(region *Symbol, predicate func(regionDependencyState) bool, reason string) {
	if a.currentRegionRefs == nil || region == nil {
		return
	}
	for sym, state := range a.currentRegionRefs {
		nextState, changed := invalidateRegionDependencyInState(state, region, predicate, reason)
		if changed {
			a.currentRegionRefs[sym] = nextState
		}
	}
}

func (a *Analyzer) invalidateRegionMarks(region *Symbol, predicate func(regionMarkState) bool, reason string) {
	if a.currentRegionMarks == nil || region == nil {
		return
	}
	for sym, state := range a.currentRegionMarks {
		if state.Region != region || !state.Valid {
			continue
		}
		if predicate != nil && !predicate(state) {
			continue
		}
		state.Valid = false
		state.InvalidatedBy = reason
		a.currentRegionMarks[sym] = state
	}
}
