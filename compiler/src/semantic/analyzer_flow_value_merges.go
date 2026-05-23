package semantic

func mergeAffineValueStates(dst map[affineValueKey]affineValueState, src map[affineValueKey]affineValueState) map[affineValueKey]affineValueState {
	if dst == nil && src == nil {
		return nil
	}
	if dst == nil {
		dst = map[affineValueKey]affineValueState{}
	}
	for key, state := range src {
		existing, ok := dst[key]
		if !ok {
			dst[key] = state
			continue
		}
		if existing.ConsumedBy == "" && state.ConsumedBy != "" {
			existing.ConsumedBy = state.ConsumedBy
		}
		if existing.LiveProtocolType == nil && state.LiveProtocolType != nil {
			existing.LiveProtocolType = state.LiveProtocolType
		}
		if existing.LiveProtocolDescription == "" && state.LiveProtocolDescription != "" {
			existing.LiveProtocolDescription = state.LiveProtocolDescription
		}
		// Definite-assignment meet: a local is uninitialized after a join if it
		// is uninitialized on either incoming path.
		if state.Uninitialized {
			existing.Uninitialized = true
		}
		dst[key] = existing
	}
	return dst
}

func mergeBorrowedOwnerRefBindings(dst map[*Symbol]borrowedOwnerRefState, src map[*Symbol]borrowedOwnerRefState) map[*Symbol]borrowedOwnerRefState {
	if dst == nil || src == nil {
		return nil
	}
	merged := make(map[*Symbol]borrowedOwnerRefState, len(dst))
	for sym, state := range dst {
		srcState, ok := src[sym]
		if !ok {
			continue
		}
		mergedState, ok := mergeBorrowedOwnerRefState(state, srcState)
		if ok {
			merged[sym] = mergedState
		}
	}
	return merged
}

func intersectRegionRefSummary(dst regionRefState, src regionRefState) (regionRefState, bool) {
	merged := regionRefState{}
	if hasRegionParamDependencies(dst) && hasRegionParamDependencies(src) {
		forEachRegionParamDep(dst, func(index int) {
			if !regionRefStateHasParamDep(src, index) {
				return
			}
			appendRegionParamDep(&merged, index)
		})
	}
	if len(dst.Fields) != 0 && len(src.Fields) != 0 {
		for key, child := range dst.Fields {
			srcChild, ok := src.Fields[key]
			if !ok {
				continue
			}
			mergedChild, ok := intersectRegionRefSummary(child, srcChild)
			if !ok {
				continue
			}
			if merged.Fields == nil {
				merged.Fields = map[string]regionRefState{}
			}
			merged.Fields[key] = mergedChild
		}
	}
	if !hasRegionProvenance(merged) {
		return regionRefState{}, false
	}
	return merged, true
}

func functionValueMergeName(dst string, src string) string {
	if dst == src {
		return dst
	}
	if dst == "" && src == "" {
		return ""
	}
	return "func"
}

func mergeFunctionValueExplicitParamNames(dst []string, src []string) []string {
	if len(dst) == 0 || len(dst) != len(src) {
		return nil
	}
	for i := range dst {
		if dst[i] != src[i] {
			return nil
		}
	}
	return append([]string(nil), dst...)
}

func sameFuncGuardEffects(left []FuncGuardEffect, right []FuncGuardEffect) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameFuncPoststates(left []FuncPoststate, right []FuncPoststate) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Condition != right[i].Condition || left[i].ParamIndex != right[i].ParamIndex || left[i].Kind != right[i].Kind || left[i].RefState != right[i].RefState {
			return false
		}
		if !sameStringSlice(left[i].StateCases, right[i].StateCases) {
			return false
		}
		if !borrowReturnAnnotationPathEqual(left[i].Path, right[i].Path) {
			return false
		}
	}
	return true
}

func functionValueMergeCompatible(dst *FuncType, src *FuncType) bool {
	if dst == nil || src == nil {
		return false
	}
	if dst.Variadic != src.Variadic || funcTypeExplicitParamCount(dst) != funcTypeExplicitParamCount(src) || len(dst.ImplicitParamNames) != len(src.ImplicitParamNames) || len(dst.GenericParams) != len(src.GenericParams) || len(dst.TypeParams) != len(src.TypeParams) || len(dst.RefStorageParams) != len(src.RefStorageParams) || len(dst.RefStateParams) != len(src.RefStateParams) || len(dst.RegionParams) != len(src.RegionParams) || len(dst.PermissionParams) != len(src.PermissionParams) || len(dst.UsedPermissionParams) != len(src.UsedPermissionParams) || len(dst.ShapeParams) != len(src.ShapeParams) || len(dst.FreshReturnShapeParams) != len(src.FreshReturnShapeParams) || len(dst.Params) != len(src.Params) || !SameType(dst.Return, src.Return) {
		return false
	}
	if !sameStringSlice(dst.ImplicitParamNames, src.ImplicitParamNames) || !sameStringSlice(dst.TypeParams, src.TypeParams) || !sameStringSlice(dst.RefStorageParams, src.RefStorageParams) || !sameStringSlice(dst.RefStateParams, src.RefStateParams) || !sameStringSlice(dst.RegionParams, src.RegionParams) || !sameStringSlice(dst.PermissionParams, src.PermissionParams) || !sameStringSlice(dst.UsedPermissionParams, src.UsedPermissionParams) || !sameStringSlice(dst.ShapeParams, src.ShapeParams) || !sameStringSlice(dst.FreshReturnShapeParams, src.FreshReturnShapeParams) {
		return false
	}
	for i := range dst.GenericParams {
		if !sameGenericParam(dst.GenericParams[i], src.GenericParams[i]) {
			return false
		}
	}
	for i := range dst.Params {
		if !SameType(dst.Params[i], src.Params[i]) {
			return false
		}
	}
	return true
}

func (a *Analyzer) mergeFunctionValueTypes(dst *FuncType, src *FuncType) (*FuncType, bool) {
	if !functionValueMergeCompatible(dst, src) {
		return nil, false
	}
	merged := a.cloneFunctionValueType(dst)
	if merged == nil {
		return nil, false
	}
	merged.Name = functionValueMergeName(dst.Name, src.Name)
	merged.ExplicitParamNames = mergeFunctionValueExplicitParamNames(dst.ExplicitParamNames, src.ExplicitParamNames)
	merged.DeclaredPermissionRefs = mergePermissionRefs(dst.DeclaredPermissionRefs, src.DeclaredPermissionRefs)
	merged.DeclaredPermissions = mergePermissionFamilies(dst.DeclaredPermissions, src.DeclaredPermissions)
	merged.PermissionRefs = mergePermissionRefs(dst.PermissionRefs, src.PermissionRefs)
	merged.Permissions = mergePermissionFamilies(dst.Permissions, src.Permissions)
	if sameFuncGuardEffects(dst.GuardEffects, src.GuardEffects) {
		merged.GuardEffects = cloneFuncGuardEffects(dst.GuardEffects)
	} else {
		merged.GuardEffects = nil
	}
	if sameFuncPoststates(dst.Poststates, src.Poststates) {
		merged.Poststates = cloneFuncPoststates(dst.Poststates)
	} else {
		merged.Poststates = nil
	}
	merged.ReturnProvenance = regionRefState{}
	merged.ReturnProvenanceKnown = dst.ReturnProvenanceKnown && src.ReturnProvenanceKnown
	if merged.ReturnProvenanceKnown {
		if state, ok := intersectRegionRefSummary(dst.ReturnProvenance, src.ReturnProvenance); ok {
			merged.ReturnProvenance = state
		}
	}
	merged.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	merged.ReturnBorrowedOwnerRefsKnown = dst.ReturnBorrowedOwnerRefsKnown && src.ReturnBorrowedOwnerRefsKnown
	if merged.ReturnBorrowedOwnerRefsKnown {
		if summary, ok := mergeBorrowedOwnerRefSummary(dst.ReturnBorrowedOwnerRefs, src.ReturnBorrowedOwnerRefs); ok {
			merged.ReturnBorrowedOwnerRefs = summary
		}
	}
	merged.SinkParams = nil
	merged.SinkParamsKnown = dst.SinkParamsKnown && src.SinkParamsKnown && len(dst.SinkParams) == len(src.SinkParams)
	if merged.SinkParamsKnown {
		merged.SinkParams = make([]bool, len(dst.SinkParams))
		for i := range dst.SinkParams {
			merged.SinkParams[i] = dst.SinkParams[i] && src.SinkParams[i]
		}
	}
	merged.ReturnIsolation = ReturnIsolationSummary{}
	merged.ReturnIsolationKnown = dst.ReturnIsolationKnown && src.ReturnIsolationKnown && returnIsolationSummariesEqual(dst.ReturnIsolation, src.ReturnIsolation)
	if merged.ReturnIsolationKnown {
		merged.ReturnIsolation = dst.ReturnIsolation
	}
	return merged, true
}

func (a *Analyzer) mergeFunctionValueBindings(dst map[*Symbol]*FuncType, src map[*Symbol]*FuncType) map[*Symbol]*FuncType {
	if dst == nil || src == nil {
		return nil
	}
	merged := make(map[*Symbol]*FuncType, len(dst))
	for sym, fn := range dst {
		srcFn, ok := src[sym]
		if !ok {
			continue
		}
		mergedFn, ok := a.mergeFunctionValueTypes(fn, srcFn)
		if ok {
			merged[sym] = mergedFn
		}
	}
	return merged
}
