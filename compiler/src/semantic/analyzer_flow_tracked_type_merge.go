package semantic

type trackedValueMergePair struct {
	Left  Type
	Right Type
}

func (a *Analyzer) mergeTrackedValueTypes(left Type, right Type) (Type, bool) {
	return a.mergeTrackedValueTypesWithSeen(left, right, map[trackedValueMergePair]Type{})
}

func (a *Analyzer) mergeTrackedValueTypesWithSeen(left Type, right Type, seen map[trackedValueMergePair]Type) (Type, bool) {
	if left == nil || right == nil {
		return nil, false
	}
	if SameType(left, right) {
		return a.cloneTrackedValueType(left), true
	}
	if merged := MergeTypes(left, right); !IsInvalidType(merged) {
		return merged, true
	}
	pair := trackedValueMergePair{Left: left, Right: right}
	if merged, ok := seen[pair]; ok {
		return merged, true
	}
	switch lt := left.(type) {
	case *AggregateStateType:
		rt, ok := right.(*AggregateStateType)
		if !ok {
			return nil, false
		}
		states, ok := mergeAggregateStateLists(aggregateStateStates(lt), aggregateStateStates(rt))
		if !ok {
			return nil, false
		}
		base, ok := a.mergeTrackedValueTypesWithSeen(lt.Base, rt.Base, seen)
		if !ok || base == nil {
			return nil, false
		}
		merged := cloneAggregateStateWithBase(base, states)
		seen[pair] = merged
		return merged, true
	case *RefType:
		rt, ok := right.(*RefType)
		if !ok {
			return nil, false
		}
		storage, explicit, okStorage := mergeRefStorages(lt.Storage, rt.Storage, lt.ExplicitStorage, rt.ExplicitStorage)
		region, okRegion := mergeRefRegions(lt.Region, rt.Region)
		state, okState := mergeRefStates(lt.State, rt.State)
		if !okStorage || !okRegion || !okState {
			return nil, false
		}
		merged := &RefType{Mutable: lt.Mutable && rt.Mutable, State: state, Storage: storage, Region: region, ExplicitStorage: explicit}
		seen[pair] = merged
		elem, ok := a.mergeTrackedValueTypesWithSeen(lt.Elem, rt.Elem, seen)
		if !ok || elem == nil {
			return nil, false
		}
		merged.Elem = elem
		return merged, true
	case *ArrayType:
		rt, ok := right.(*ArrayType)
		if !ok || lt.SurfaceName != rt.SurfaceName || !arraySizesEqual(lt, rt) {
			return nil, false
		}
		elem, ok := a.mergeTrackedValueTypesWithSeen(lt.Elem, rt.Elem, seen)
		if !ok || elem == nil {
			return nil, false
		}
		merged := *lt
		merged.Elem = elem
		seen[pair] = &merged
		return &merged, true
	case *DArrayType:
		rt, ok := right.(*DArrayType)
		if !ok || lt.SurfaceName != rt.SurfaceName || !SameShape(lt.Shape, rt.Shape) {
			return nil, false
		}
		elem, ok := a.mergeTrackedValueTypesWithSeen(lt.Elem, rt.Elem, seen)
		if !ok || elem == nil {
			return nil, false
		}
		merged := *lt
		merged.Elem = elem
		seen[pair] = &merged
		return &merged, true
	case *ViewType:
		rt, ok := right.(*ViewType)
		if !ok {
			return nil, false
		}
		elem, ok := a.mergeTrackedValueTypesWithSeen(lt.Elem, rt.Elem, seen)
		if !ok || elem == nil {
			return nil, false
		}
		merged := *lt
		if !viewBoundsEqual(lt, rt) {
			merged.Begin = ""
			merged.End = ""
		}
		merged.Elem = elem
		seen[pair] = &merged
		return &merged, true
	case *DArrayViewType:
		rt, ok := right.(*DArrayViewType)
		if !ok || lt.SurfaceName != rt.SurfaceName {
			return nil, false
		}
		elem, ok := a.mergeTrackedValueTypesWithSeen(lt.Elem, rt.Elem, seen)
		if !ok || elem == nil {
			return nil, false
		}
		merged := *lt
		if lt.Begin != rt.Begin || lt.End != rt.End {
			merged.Begin = ""
			merged.End = ""
		}
		merged.Elem = elem
		seen[pair] = &merged
		return &merged, true
	case *OptionalType:
		rt, ok := right.(*OptionalType)
		if !ok {
			return nil, false
		}
		value, ok := a.mergeTrackedValueTypesWithSeen(lt.Value, rt.Value, seen)
		if !ok || value == nil {
			return nil, false
		}
		merged := &OptionalType{Value: value}
		seen[pair] = merged
		return merged, true
	case *DictType:
		rt, ok := right.(*DictType)
		if !ok || lt.SurfaceName != rt.SurfaceName {
			return nil, false
		}
		key, ok := a.mergeTrackedValueTypesWithSeen(lt.Key, rt.Key, seen)
		if !ok || key == nil {
			return nil, false
		}
		value, ok := a.mergeTrackedValueTypesWithSeen(lt.Value, rt.Value, seen)
		if !ok || value == nil {
			return nil, false
		}
		merged := &DictType{Key: key, Value: value, SurfaceName: lt.SurfaceName}
		seen[pair] = merged
		return merged, true
	case *StructType:
		rt, ok := right.(*StructType)
		if !ok || lt.Name != rt.Name {
			return nil, false
		}
		merged := *lt
		merged.Fields = cloneStructFields(lt.Fields)
		mergedPtr := &merged
		seen[pair] = mergedPtr
		for name, leftField := range lt.Fields {
			rightField, ok := rt.Fields[name]
			if !ok {
				return nil, false
			}
			if SameType(leftField.Type, rightField.Type) {
				continue
			}
			fieldType, ok := a.mergeTrackedValueTypesWithSeen(leftField.Type, rightField.Type, seen)
			if !ok || fieldType == nil {
				return nil, false
			}
			leftField.Type = fieldType
			mergedPtr.Fields[name] = leftField
		}
		return mergedPtr, true
	case *GenericInstanceType:
		rt, ok := right.(*GenericInstanceType)
		if !ok || lt.Name != rt.Name || len(lt.Args) != len(rt.Args) {
			return nil, false
		}
		merged := *lt
		merged.Args = append([]Type(nil), lt.Args...)
		mergedPtr := &merged
		seen[pair] = mergedPtr
		for i := range lt.Args {
			if SameType(lt.Args[i], rt.Args[i]) {
				continue
			}
			argType, ok := a.mergeTrackedValueTypesWithSeen(lt.Args[i], rt.Args[i], seen)
			if !ok || argType == nil {
				return nil, false
			}
			mergedPtr.Args[i] = argType
		}
		if SameType(lt.Base, rt.Base) {
			mergedPtr.Base = a.cloneTrackedValueType(lt.Base)
			return mergedPtr, true
		}
		base, ok := a.mergeTrackedValueTypesWithSeen(lt.Base, rt.Base, seen)
		if !ok || base == nil {
			return nil, false
		}
		mergedPtr.Base = base
		return mergedPtr, true
	case *PackedEnumStoreType:
		rt, ok := right.(*PackedEnumStoreType)
		if !ok || lt.Name != rt.Name || lt.Enum != rt.Enum {
			return nil, false
		}
		if lt.State == nil && rt.State == nil {
			merged := *lt
			seen[pair] = &merged
			return &merged, true
		}
		state, ok := a.mergeTrackedValueTypesWithSeen(lt.State, rt.State, seen)
		if !ok || state == nil {
			return nil, false
		}
		merged := *lt
		merged.State = state
		seen[pair] = &merged
		return &merged, true
	case *DStrType:
		rt, ok := right.(*DStrType)
		if !ok || lt.SurfaceName != rt.SurfaceName {
			return nil, false
		}
		merged := *lt
		if !SameShape(lt.Shape, rt.Shape) {
			merged.Shape = &WildcardShape{}
		}
		seen[pair] = &merged
		return &merged, true
	case *SViewType:
		rt, ok := right.(*SViewType)
		if !ok {
			return nil, false
		}
		merged := *lt
		if lt.Begin != rt.Begin || lt.End != rt.End {
			merged.Begin = ""
			merged.End = ""
		}
		seen[pair] = &merged
		return &merged, true
	default:
		return nil, false
	}
}

func (a *Analyzer) computeCallArgPoststateTrackedType(original Type, argSteps []borrowReturnAnnotationStep, poststates []FuncPoststate, outcomeKnown bool, outcomeValue bool) (Type, bool) {
	if original == nil {
		return nil, false
	}
	if len(poststates) == 0 {
		widened, ok := a.widenNamedStatesDeepAtPath(original, argSteps)
		if !ok || widened == nil || SameType(widened, original) {
			return nil, false
		}
		return widened, true
	}
	baseline := original
	baselineChanged := false
	if widened, ok := a.widenNamedStatesDeepAtPath(baseline, argSteps); ok && widened != nil {
		baseline = widened
		baselineChanged = true
	}
	updated := baseline
	always, whenTrue, whenFalse := splitFuncPoststatesByCondition(poststates)
	changed := baselineChanged
	if next, applied := a.applyFuncPoststateListAtArgPath(original, updated, argSteps, always); applied && next != nil {
		updated = next
		changed = true
	}
	if len(whenTrue) == 0 && len(whenFalse) == 0 {
		if !changed {
			return nil, false
		}
		return updated, true
	}
	if outcomeKnown {
		branch := updated
		matching := whenFalse
		if outcomeValue {
			matching = whenTrue
		}
		if next, applied := a.applyFuncPoststateListAtArgPath(original, branch, argSteps, matching); applied && next != nil {
			branch = next
			changed = true
		}
		if !changed {
			return nil, false
		}
		return branch, true
	}
	trueBranch := updated
	falseBranch := updated
	branchChanged := false
	if next, applied := a.applyFuncPoststateListAtArgPath(original, trueBranch, argSteps, whenTrue); applied && next != nil {
		trueBranch = next
		branchChanged = true
	}
	if next, applied := a.applyFuncPoststateListAtArgPath(original, falseBranch, argSteps, whenFalse); applied && next != nil {
		falseBranch = next
		branchChanged = true
	}
	joined, ok := a.mergeTrackedValueTypes(trueBranch, falseBranch)
	if !ok || joined == nil || IsInvalidType(joined) {
		joined = updated
	}
	if !changed && !branchChanged {
		return nil, false
	}
	return joined, true
}
