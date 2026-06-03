package semantic

func matchTypePattern(pattern, actual Type) bool {
	if pattern == nil || actual == nil {
		return pattern == actual
	}
	if IsInvalidType(pattern) || IsInvalidType(actual) {
		return true
	}
	if patternRuntimeCompatible(pattern, actual) {
		return true
	}
	if _, ok := pattern.(*TypeParamType); ok {
		return true
	}
	if _, ok := pattern.(*ConstParamType); ok {
		_, ok = actual.(*ConstValueType)
		return ok
	}
	if _, ok := pattern.(*RefStorageParamType); ok {
		_, ok = actual.(*RefStorageValueType)
		return ok
	}
	switch p := pattern.(type) {
	case *NeverType:
		_, ok := actual.(*NeverType)
		return ok
	case *BuiltinType:
		a, ok := actual.(*BuiltinType)
		return ok && p.Name == a.Name
	case *StructStateCaseType, *StructStateSetType:
		return namedStateTypeAssignable(pattern, actual)
	case *NullType:
		_, ok := actual.(*NullType)
		return ok
	case *ErrorSetType:
		a, ok := actual.(*ErrorSetType)
		return ok && ErrorSetAssignable(p, a)
	case *ErrorUnionType:
		if a, ok := actual.(*ErrorUnionType); ok {
			return matchTypePattern(p.Value, a.Value) && matchTypePattern(p.Errors, a.Errors)
		}
		return matchTypePattern(p.Value, actual)
	case *OptionalType:
		a, ok := actual.(*OptionalType)
		return ok && matchTypePattern(p.Value, a.Value)
	case *TupleType:
		a, ok := actual.(*TupleType)
		if !ok || len(p.Fields) != len(a.Fields) {
			return false
		}
		for i := range p.Fields {
			if !matchTypePattern(p.Fields[i].Type, a.Fields[i].Type) {
				return false
			}
		}
		return true
	case *ConstEnumType:
		a, ok := actual.(*ConstEnumType)
		return ok && p.Name == a.Name
	case *RefType:
		a, ok := actual.(*RefType)
		if !ok {
			return false
		}
		if p.Mutable && !a.Mutable && p.State != RefStateNull && a.State != RefStateNull {
			return false
		}
		if !refStateAssignable(p.State, a.State) {
			return false
		}
		if p.StorageParam != "" || a.StorageParam != "" {
			if p.StorageParam != a.StorageParam {
				return false
			}
		} else if !refStorageAssignable(p.Storage, a.Storage, p.ExplicitStorage, a.ExplicitStorage) {
			return false
		}
		if !refRegionAssignable(p.Region, a.Region) {
			return false
		}
		return matchTypePattern(p.Elem, a.Elem)
	case *ArrayType:
		a, ok := actual.(*ArrayType)
		return ok && arraySizesEqual(p, a) && matchTypePattern(p.Elem, a.Elem)
	case *DArrayType:
		a, ok := actual.(*DArrayType)
		return ok && matchTypePattern(p.Elem, a.Elem) && shapeMatchesPattern(p.Shape, a.Shape)
	case *ViewType:
		a, ok := actual.(*ViewType)
		return ok && matchTypePattern(p.Elem, a.Elem) && viewBoundsMatch(p, a)
	case *DArrayViewType:
		a, ok := actual.(*DArrayViewType)
		return ok && matchTypePattern(p.Elem, a.Elem)
	case *DStrType:
		a, ok := actual.(*DStrType)
		return ok && shapeMatchesPattern(p.Shape, a.Shape)
	case *DictType:
		a, ok := actual.(*DictType)
		return ok && matchTypePattern(p.Key, a.Key) && matchTypePattern(p.Value, a.Value)
	case *DictEntryType:
		a, ok := actual.(*DictEntryType)
		return ok && p.Mutable == a.Mutable && matchTypePattern(p.Dict, a.Dict)
	case *SViewType:
		_, ok := actual.(*SViewType)
		return ok
	case *PackedEnumStoreType:
		a, ok := actual.(*PackedEnumStoreType)
		return ok && p.Name == a.Name && matchTypePattern(p.State, a.State)
	case *TreeStoreType:
		a, ok := actual.(*TreeStoreType)
		return ok && p.Name == a.Name && matchTypePattern(p.State, a.State)
	case *PackedVariantViewType:
		a, ok := actual.(*PackedVariantViewType)
		return ok && SameType(p.Enum, a.Enum) && p.Variant != nil && a.Variant != nil && p.Variant.Name == a.Variant.Name
	case *TreeVariantViewType:
		a, ok := actual.(*TreeVariantViewType)
		return ok && SameType(p.Category, a.Category) && p.Variant != nil && a.Variant != nil && p.Variant.Name == a.Variant.Name
	case *TreeType:
		a, ok := actual.(*TreeType)
		return ok && p.Name == a.Name
	case *TreeCategoryType:
		a, ok := actual.(*TreeCategoryType)
		return ok && p.Name == a.Name
	case *TreeBlockType:
		a, ok := actual.(*TreeBlockType)
		return ok && p.Name == a.Name
	case *TreeStructType:
		a, ok := actual.(*TreeStructType)
		return ok && p.Name == a.Name
	case *EnumType:
		a, ok := actual.(*EnumType)
		return ok && p.Name == a.Name
	case *StructType:
		a, ok := actual.(*StructType)
		return ok && p.Name == a.Name
	case *OpaqueType:
		a, ok := actual.(*OpaqueType)
		return ok && p.Name == a.Name
	case *GenericInstanceType:
		a, ok := actual.(*GenericInstanceType)
		if !ok || p.Name != a.Name || len(p.Args) != len(a.Args) {
			return false
		}
		for i := range p.Args {
			if !matchTypePattern(p.Args[i], a.Args[i]) {
				return false
			}
		}
		return matchTypePattern(p.Base, a.Base)
	case *AggregateStateType:
		a, ok := actual.(*AggregateStateType)
		if !ok {
			return false
		}
		if !aggregateStateListAssignable(aggregateStateStates(p), aggregateStateStates(a)) {
			return false
		}
		return matchTypePattern(p.Base, a.Base)
	case *FuncType:
		a, ok := actual.(*FuncType)
		if !ok || p.Variadic != a.Variadic || funcTypeExplicitParamCount(p) != funcTypeExplicitParamCount(a) || len(p.ImplicitParamNames) != len(a.ImplicitParamNames) || len(p.RegionParams) != len(a.RegionParams) || len(p.ShapeParams) != len(a.ShapeParams) || len(p.FreshReturnShapeParams) != len(a.FreshReturnShapeParams) || len(p.Params) != len(a.Params) {
			return false
		}
		if len(p.ExplicitParamNames) != 0 {
			if len(p.ExplicitParamNames) != len(a.ExplicitParamNames) {
				return false
			}
			for i := range p.ExplicitParamNames {
				if p.ExplicitParamNames[i] != a.ExplicitParamNames[i] {
					return false
				}
			}
		}
		for i := range p.ImplicitParamNames {
			if p.ImplicitParamNames[i] != a.ImplicitParamNames[i] {
				return false
			}
		}
		for i := range p.RegionParams {
			if p.RegionParams[i] != a.RegionParams[i] {
				return false
			}
		}
		if _, ok := funcTypeHasSinglePermissionRowParam(p); !ok {
			if len(p.Permissions) != len(a.Permissions) {
				return false
			}
			for i := range p.Permissions {
				if p.Permissions[i] != a.Permissions[i] {
					return false
				}
			}
		}
		for i := range p.FreshReturnShapeParams {
			if p.FreshReturnShapeParams[i] != a.FreshReturnShapeParams[i] {
				return false
			}
		}
		for i := range p.Params {
			if !matchTypePattern(a.Params[i], p.Params[i]) {
				return false
			}
		}
		return matchTypePattern(p.Return, a.Return)
	default:
		return SameType(pattern, actual)
	}
}
