package semantic

type specializedTypeMergeKey struct {
	dst Type
	src Type
}

func (a *Analyzer) mergeSpecializedValueTypes(dst Type, src Type) (Type, bool) {
	return a.mergeSpecializedValueTypesWithSeen(dst, src, map[specializedTypeMergeKey]Type{}, map[Type]Type{})
}

func (a *Analyzer) mergeSpecializedValueTypesWithSeen(dst Type, src Type, seen map[specializedTypeMergeKey]Type, cloneSeen map[Type]Type) (Type, bool) {
	if merged, ok := a.mergeTrackedNamedStateValueTypes(dst, src); ok {
		return merged, true
	}
	if dst == nil || src == nil || !SameType(dst, src) {
		return nil, false
	}
	if cloneSeen == nil {
		cloneSeen = map[Type]Type{}
	}
	key := specializedTypeMergeKey{dst: dst, src: src}
	if merged, ok := seen[key]; ok {
		return merged, true
	}
	if dstFunc, ok := dst.(*FuncType); ok {
		srcFunc, ok := src.(*FuncType)
		if !ok {
			return nil, false
		}
		merged, ok := a.mergeFunctionValueTypes(dstFunc, srcFunc)
		if !ok {
			return nil, false
		}
		seen[key] = merged
		return merged, true
	}
	switch tt := dst.(type) {
	case *StructType:
		srcStruct, ok := src.(*StructType)
		if !ok {
			return nil, false
		}
		fields := cloneStructFields(tt.Fields)
		mergedStruct := cloneStructTypeWithFields(tt, fields)
		seen[key] = mergedStruct
		for name, field := range tt.Fields {
			srcField, ok := srcStruct.Fields[name]
			if !ok {
				continue
			}
			mergedFieldType, ok := a.mergeSpecializedValueTypesWithSeen(field.Type, srcField.Type, seen, cloneSeen)
			if !ok {
				continue
			}
			field.Type = mergedFieldType
			fields[name] = field
		}
		return mergedStruct, true
	case *GenericInstanceType:
		srcInstance, ok := src.(*GenericInstanceType)
		if !ok {
			return nil, false
		}
		if len(tt.Args) != len(srcInstance.Args) {
			return nil, false
		}
		cloned := *tt
		cloned.Args = make([]Type, len(tt.Args))
		mergedInstance := &cloned
		seen[key] = mergedInstance
		mergedBase, ok := a.mergeSpecializedValueTypesWithSeen(tt.Base, srcInstance.Base, seen, cloneSeen)
		if !ok {
			delete(seen, key)
			return nil, false
		}
		mergedInstance.Base = mergedBase
		for i := range tt.Args {
			mergedArg, ok := a.mergeSpecializedValueTypesWithSeen(tt.Args[i], srcInstance.Args[i], seen, cloneSeen)
			if !ok {
				mergedInstance.Args[i] = a.cloneTrackedValueTypeWithSeen(tt.Args[i], cloneSeen)
				continue
			}
			mergedInstance.Args[i] = mergedArg
		}
		return mergedInstance, true
	case *RefType:
		srcRef, ok := src.(*RefType)
		if !ok {
			return nil, false
		}
		cloned := *tt
		mergedRef := &cloned
		seen[key] = mergedRef
		mergedElem, ok := a.mergeSpecializedValueTypesWithSeen(tt.Elem, srcRef.Elem, seen, cloneSeen)
		if !ok {
			delete(seen, key)
			return nil, false
		}
		mergedRef.Elem = mergedElem
		return mergedRef, true
	case *ArrayType:
		srcArray, ok := src.(*ArrayType)
		if !ok {
			return nil, false
		}
		cloned := *tt
		mergedArray := &cloned
		seen[key] = mergedArray
		mergedElem, ok := a.mergeSpecializedValueTypesWithSeen(tt.Elem, srcArray.Elem, seen, cloneSeen)
		if !ok {
			delete(seen, key)
			return nil, false
		}
		mergedArray.Elem = mergedElem
		return mergedArray, true
	case *DArrayType:
		srcArray, ok := src.(*DArrayType)
		if !ok {
			return nil, false
		}
		cloned := *tt
		mergedArray := &cloned
		seen[key] = mergedArray
		mergedElem, ok := a.mergeSpecializedValueTypesWithSeen(tt.Elem, srcArray.Elem, seen, cloneSeen)
		if !ok {
			delete(seen, key)
			return nil, false
		}
		mergedArray.Elem = mergedElem
		return mergedArray, true
	case *OptionalType:
		srcOpt, ok := src.(*OptionalType)
		if !ok {
			return nil, false
		}
		cloned := *tt
		mergedOptional := &cloned
		seen[key] = mergedOptional
		mergedValue, ok := a.mergeSpecializedValueTypesWithSeen(tt.Value, srcOpt.Value, seen, cloneSeen)
		if !ok {
			delete(seen, key)
			return nil, false
		}
		mergedOptional.Value = mergedValue
		return mergedOptional, true
	case *ViewType:
		srcView, ok := src.(*ViewType)
		if !ok {
			return nil, false
		}
		cloned := *tt
		mergedView := &cloned
		seen[key] = mergedView
		mergedElem, ok := a.mergeSpecializedValueTypesWithSeen(tt.Elem, srcView.Elem, seen, cloneSeen)
		if !ok {
			delete(seen, key)
			return nil, false
		}
		mergedView.Elem = mergedElem
		return mergedView, true
	case *DArrayViewType:
		srcView, ok := src.(*DArrayViewType)
		if !ok {
			return nil, false
		}
		cloned := *tt
		mergedView := &cloned
		seen[key] = mergedView
		mergedElem, ok := a.mergeSpecializedValueTypesWithSeen(tt.Elem, srcView.Elem, seen, cloneSeen)
		if !ok {
			delete(seen, key)
			return nil, false
		}
		mergedView.Elem = mergedElem
		return mergedView, true
	case *DictType:
		srcDict, ok := src.(*DictType)
		if !ok {
			return nil, false
		}
		cloned := *tt
		mergedDict := &cloned
		seen[key] = mergedDict
		mergedKey, ok := a.mergeSpecializedValueTypesWithSeen(tt.Key, srcDict.Key, seen, cloneSeen)
		if !ok {
			delete(seen, key)
			return nil, false
		}
		mergedValue, ok := a.mergeSpecializedValueTypesWithSeen(tt.Value, srcDict.Value, seen, cloneSeen)
		if !ok {
			delete(seen, key)
			return nil, false
		}
		mergedDict.Key = mergedKey
		mergedDict.Value = mergedValue
		return mergedDict, true
	default:
		cloned := a.cloneTrackedValueTypeWithSeen(dst, cloneSeen)
		seen[key] = cloned
		return cloned, true
	}
}

func (a *Analyzer) mergeSpecializedValueTypeBindings(dst map[*Symbol]Type, src map[*Symbol]Type) map[*Symbol]Type {
	if dst == nil || src == nil {
		return nil
	}
	merged := make(map[*Symbol]Type, len(dst))
	seen := map[Type]Type{}
	for sym, typ := range dst {
		srcType, ok := src[sym]
		if !ok {
			continue
		}
		mergedType, ok := a.mergeSpecializedValueTypes(typ, srcType)
		if !ok {
			continue
		}
		if normalized, ok := a.specializeCallbackCarryingType(sym.Type, mergedType); ok {
			merged[sym] = normalized
			continue
		}
		merged[sym] = a.cloneTrackedValueTypeWithSeen(mergedType, seen)
	}
	return merged
}

func (a *Analyzer) cloneSpecializedValueTypeMap(src map[*Symbol]Type) map[*Symbol]Type {
	return a.cloneTrackedValueTypeMapWithSeen(src, map[Type]Type{})
}

func (a *Analyzer) intersectSpecializedValueTypeFlows(flows ...map[*Symbol]Type) (map[*Symbol]Type, bool) {
	var merged map[*Symbol]Type
	mergedAny := false
	for _, flow := range flows {
		if !mergedAny {
			merged = a.cloneSpecializedValueTypeMap(flow)
			mergedAny = true
			continue
		}
		merged = a.mergeSpecializedValueTypeBindings(merged, flow)
	}
	if !mergedAny {
		return nil, false
	}
	return merged, true
}

func (a *Analyzer) cloneFunctionValueMap(src map[*Symbol]*FuncType) map[*Symbol]*FuncType {
	if src == nil {
		return nil
	}
	cloned := make(map[*Symbol]*FuncType, len(src))
	for sym, fn := range src {
		cloned[sym] = a.cloneFunctionValueType(fn)
	}
	return cloned
}

func (a *Analyzer) intersectFunctionValueFlows(flows ...map[*Symbol]*FuncType) (map[*Symbol]*FuncType, bool) {
	var merged map[*Symbol]*FuncType
	mergedAny := false
	for _, flow := range flows {
		if !mergedAny {
			merged = a.cloneFunctionValueMap(flow)
			mergedAny = true
			continue
		}
		merged = a.mergeFunctionValueBindings(merged, flow)
	}
	if !mergedAny {
		return nil, false
	}
	return merged, true
}
