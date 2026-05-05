package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

func firstInvalidRegionDependency(state regionRefState) (*Symbol, regionDependencyState, bool) {
	for region, dep := range state.Deps {
		if !dep.Valid {
			return region, dep, true
		}
	}
	return nil, regionDependencyState{}, false
}

func regionRefFieldsIdentity(fields map[string]regionRefState) uintptr {
	if len(fields) == 0 {
		return 0
	}
	return reflect.ValueOf(fields).Pointer()
}

func abstractParamOnlyRegionRefState(state regionRefState) (regionRefState, bool) {
	if state.ParamOnlySummary {
		return cloneRegionRefState(state), true
	}
	filtered, ok, _ := abstractParamOnlyRegionRefStateWithSeen(state, map[uintptr]struct{}{})
	if !ok {
		return regionRefState{}, false
	}
	return filtered, true
}

func abstractParamOnlyRegionRefStateWithSeen(state regionRefState, seen map[uintptr]struct{}) (regionRefState, bool, bool) {
	if !hasRegionProvenance(state) {
		return regionRefState{}, false, false
	}
	out := state
	changed := len(state.Deps) != 0 || len(state.StoreDeps) != 0 || !state.PackedStoreSummaryKnown || !state.ParamOnlySummary
	if len(state.Deps) != 0 {
		out.Deps = nil
	}
	if len(state.StoreDeps) != 0 {
		out.StoreDeps = nil
	}
	if len(state.Fields) != 0 {
		fieldsID := regionRefFieldsIdentity(state.Fields)
		if fieldsID != 0 {
			if _, ok := seen[fieldsID]; ok {
				out.Fields = nil
				if !hasRegionProvenance(out) {
					return regionRefState{}, false, false
				}
				out.PackedStoreSummaryKnown = false
				out.ParamOnlySummary = true
				return withPackedStoreProvenanceSummary(out), true, false
			}
			seen[fieldsID] = struct{}{}
			defer delete(seen, fieldsID)
		}
		fieldsCloned := false
		for name, fieldState := range state.Fields {
			filtered, ok, unchanged := abstractParamOnlyRegionRefStateWithSeen(fieldState, seen)
			if ok && unchanged {
				continue
			}
			if !fieldsCloned {
				out.Fields = cloneRegionRefFields(state.Fields)
				fieldsCloned = true
			}
			changed = true
			if !ok {
				delete(out.Fields, name)
				continue
			}
			out.Fields[name] = filtered
		}
		if fieldsCloned && len(out.Fields) == 0 {
			out.Fields = nil
		}
	}
	if !hasRegionProvenance(out) {
		return regionRefState{}, false, false
	}
	if !changed {
		return state, true, true
	}
	out.PackedStoreSummaryKnown = false
	out.ParamOnlySummary = true
	return withPackedStoreProvenanceSummary(out), true, false
}

func instantiateReturnProvenanceArgsOnly(argStates []regionRefState) (regionRefState, bool) {
	if len(argStates) == 1 {
		if !hasRegionProvenance(argStates[0]) {
			return regionRefState{}, false
		}
		return cloneRegionRefState(argStates[0]), true
	}
	instantiated := regionRefState{}
	if mergedArgs, ok := mergeRegionRefStates(argStates...); ok {
		instantiated = mergedArgs
	}
	if !hasRegionProvenance(instantiated) {
		return regionRefState{}, false
	}
	instantiated.PackedStoreSummaryKnown = false
	return withPackedStoreProvenanceSummary(instantiated), true
}

type instantiateReturnProvenanceArgResult struct {
	state    regionRefState
	ok       bool
	computed bool
}

type instantiateReturnProvenanceContext struct {
	seen      map[uintptr]struct{}
	argStates map[int]instantiateReturnProvenanceArgResult
}

func (a *Analyzer) instantiateReturnProvenance(state regionRefState, args []ast.Expr) (regionRefState, bool) {
	return a.instantiateReturnProvenanceWithContext(state, args, &instantiateReturnProvenanceContext{
		seen:      map[uintptr]struct{}{},
		argStates: map[int]instantiateReturnProvenanceArgResult{},
	})
}

func (a *Analyzer) instantiateReturnProvenanceArgState(index int, args []ast.Expr, ctx *instantiateReturnProvenanceContext) (regionRefState, bool) {
	if index < 0 || index >= len(args) {
		return regionRefState{}, false
	}
	if ctx != nil {
		if cached, ok := ctx.argStates[index]; ok && cached.computed {
			return cached.state, cached.ok
		}
	}
	argState, ok := a.regionRefStateForExpr(args[index])
	if ctx != nil {
		ctx.argStates[index] = instantiateReturnProvenanceArgResult{state: argState, ok: ok, computed: true}
	}
	return argState, ok
}

func (a *Analyzer) instantiateReturnProvenanceWithContext(state regionRefState, args []ast.Expr, ctx *instantiateReturnProvenanceContext) (regionRefState, bool) {
	if !hasRegionProvenance(state) {
		return regionRefState{}, false
	}
	if state.ParamOnlySummary {
		if index, ok := singleRegionParamDepIndex(state); ok {
			argState, argOK := a.instantiateReturnProvenanceArgState(index, args, ctx)
			if argOK && argState.ParamOnlySummary {
				return cloneRegionRefState(argState), true
			}
		}
	}
	var argStates []regionRefState
	forEachRegionParamDep(state, func(index int) {
		argState, ok := a.instantiateReturnProvenanceArgState(index, args, ctx)
		if !ok {
			return
		}
		argStates = append(argStates, argState)
	})
	var fieldStates map[string]regionRefState
	if len(state.Fields) != 0 {
		fieldsID := regionRefFieldsIdentity(state.Fields)
		if fieldsID != 0 {
			if _, ok := ctx.seen[fieldsID]; ok {
				return instantiateReturnProvenanceArgsOnly(argStates)
			}
			ctx.seen[fieldsID] = struct{}{}
			defer delete(ctx.seen, fieldsID)
		}
		for name, fieldState := range state.Fields {
			instField, ok := a.instantiateReturnProvenanceWithContext(fieldState, args, ctx)
			if !ok {
				continue
			}
			if fieldStates == nil {
				fieldStates = map[string]regionRefState{}
			}
			fieldStates[name] = instField
		}
	}
	if len(fieldStates) != 0 {
		return mergeRegionRefStatesWithExplicitFields(argStates, fieldStates)
	}
	return instantiateReturnProvenanceArgsOnly(argStates)
}

func firstLiveRegionDependency(state regionRefState) (*Symbol, regionDependencyState, bool) {
	for region, dep := range state.Deps {
		if dep.Valid {
			return region, dep, true
		}
	}
	return nil, regionDependencyState{}, false
}

func invalidateRegionDependencyInState(state regionRefState, region *Symbol, predicate func(regionDependencyState) bool, reason string) (regionRefState, bool) {
	changed := false
	depsCloned := false
	fieldsCloned := false
	if region != nil {
		if dep, ok := state.Deps[region]; ok && dep.Valid {
			if predicate == nil || predicate(dep) {
				if !depsCloned {
					state.Deps = cloneRegionDependencyStates(state.Deps)
					depsCloned = true
				}
				dep.Valid = false
				dep.InvalidatedBy = reason
				state.Deps[region] = dep
				changed = true
			}
		}
	}
	if len(state.Fields) != 0 {
		for name, fieldState := range state.Fields {
			nextField, fieldChanged := invalidateRegionDependencyInState(fieldState, region, predicate, reason)
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
	}
	if changed {
		state.PackedStoreSummaryKnown = false
		state = withPackedStoreProvenanceSummary(state)
	}
	return state, changed
}

func firstNonShareablePackedStoreDependency(state regionRefState) (*Symbol, packedStoreDependencyState, bool) {
	for store, dep := range state.StoreDeps {
		if dep.Type == nil || !IsFrozenPackedEnumStoreType(dep.Type) {
			return store, dep, true
		}
	}
	for _, fieldState := range state.Fields {
		if store, dep, ok := firstNonShareablePackedStoreDependency(fieldState); ok {
			return store, dep, true
		}
	}
	return nil, packedStoreDependencyState{}, false
}

func hasPackedStoreDependencies(state regionRefState) bool {
	if len(state.StoreDeps) != 0 {
		return true
	}
	for _, fieldState := range state.Fields {
		if hasPackedStoreDependencies(fieldState) {
			return true
		}
	}
	return false
}
