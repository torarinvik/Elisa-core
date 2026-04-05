package semantic

import "llcontext/src/ast"

func funcTypeHasSinglePermissionRowParam(fn *FuncType) (string, bool) {
	if fn == nil || len(fn.UsedPermissionParams) != 1 || len(fn.PermissionRefs) != 1 {
		return "", false
	}
	ref := fn.PermissionRefs[0]
	if ref.Member != "" || ref.Name != fn.UsedPermissionParams[0] {
		return "", false
	}
	return ref.Name, true
}

func sameAggregateStateLists(a []RefState, b []RefState) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func aggregateStateListAssignable(dst []RefState, src []RefState) bool {
	if len(dst) != len(src) {
		return false
	}
	for i := range dst {
		if !refStateAssignable(dst[i], src[i]) {
			return false
		}
	}
	return true
}

func mergeAggregateStateLists(a []RefState, b []RefState) ([]RefState, bool) {
	if len(a) != len(b) {
		return nil, false
	}
	merged := make([]RefState, len(a))
	for i := range a {
		state, ok := mergeRefStates(a[i], b[i])
		if !ok {
			return nil, false
		}
		merged[i] = state
	}
	return merged, true
}

func sameStringList(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameGenericParam(a ast.GenericParam, b ast.GenericParam) bool {
	return a.Kind == b.Kind && a.Name == b.Name && a.InterfaceBound == b.InterfaceBound && a.StateOwner == b.StateOwner && sameStringList(a.StateCases, b.StateCases)
}

func SameType(a, b Type) bool {
	if a == nil || b == nil {
		return a == b
	}
	if IsInvalidType(a) || IsInvalidType(b) {
		return true
	}
	if sameTypeRuntimeCompatible(a, b) {
		return true
	}
	if aID, ok := TryCanonicalTypeID(a); ok {
		if bID, ok := TryCanonicalTypeID(b); ok {
			return aID == bID
		}
	}
	switch ta := a.(type) {
	case *NeverType:
		_, ok := b.(*NeverType)
		return ok
	case *NullType:
		_, ok := b.(*NullType)
		return ok
	case *BuiltinType:
		tb, ok := b.(*BuiltinType)
		return ok && ta.Name == tb.Name
	case *TypeParamType:
		tb, ok := b.(*TypeParamType)
		return ok && ta.Name == tb.Name
	case *AssociatedTypeProjection:
		tb, ok := b.(*AssociatedTypeProjection)
		return ok && ta.InterfaceName == tb.InterfaceName && ta.Name == tb.Name && SameType(ta.Receiver, tb.Receiver)
	case *StructStateCaseType:
		return sameNamedStateType(ta, b)
	case *StructStateSetType:
		return sameNamedStateType(ta, b)
	case *RefStorageParamType:
		tb, ok := b.(*RefStorageParamType)
		return ok && ta.Name == tb.Name
	case *RefStorageValueType:
		tb, ok := b.(*RefStorageValueType)
		return ok && ta.Storage == tb.Storage
	case *RefStateParamType:
		tb, ok := b.(*RefStateParamType)
		return ok && ta.Name == tb.Name
	case *RefStateValueType:
		tb, ok := b.(*RefStateValueType)
		return ok && ta.State == tb.State
	case *ErrorSetType:
		tb, ok := b.(*ErrorSetType)
		return ok && ErrorSetTagsEqual(ta, tb)
	case *ErrorUnionType:
		tb, ok := b.(*ErrorUnionType)
		return ok && SameType(ta.Value, tb.Value) && SameType(ta.Errors, tb.Errors)
	case *OptionalType:
		tb, ok := b.(*OptionalType)
		return ok && SameType(ta.Value, tb.Value)
	case *ConstEnumType:
		tb, ok := b.(*ConstEnumType)
		return ok && ta.Name == tb.Name
	case *RefType:
		tb, ok := b.(*RefType)
		return ok && ta.Mutable == tb.Mutable && ta.State == tb.State && ta.StateParam == tb.StateParam && ta.Storage == tb.Storage && ta.StorageParam == tb.StorageParam && ta.Region == tb.Region && SameType(ta.Elem, tb.Elem)
	case *ArrayType:
		tb, ok := b.(*ArrayType)
		return ok && arraySizesEqual(ta, tb) && SameType(ta.Elem, tb.Elem)
	case *DArrayType:
		tb, ok := b.(*DArrayType)
		return ok && SameType(ta.Elem, tb.Elem) && SameShape(ta.Shape, tb.Shape)
	case *ViewType:
		tb, ok := b.(*ViewType)
		return ok && SameType(ta.Elem, tb.Elem) && viewBoundsEqual(ta, tb)
	case *DArrayViewType:
		tb, ok := b.(*DArrayViewType)
		return ok && SameType(ta.Elem, tb.Elem)
	case *DStrType:
		tb, ok := b.(*DStrType)
		return ok && SameShape(ta.Shape, tb.Shape)
	case *DictType:
		tb, ok := b.(*DictType)
		return ok && SameType(ta.Key, tb.Key) && SameType(ta.Value, tb.Value)
	case *SViewType:
		_, ok := b.(*SViewType)
		return ok
	case *PackedEnumStoreType:
		tb, ok := b.(*PackedEnumStoreType)
		return ok && ta.Name == tb.Name && SameType(ta.State, tb.State)
	case *TreeStoreType:
		tb, ok := b.(*TreeStoreType)
		return ok && ta.Name == tb.Name && SameType(ta.State, tb.State)
	case *PackedVariantViewType:
		tb, ok := b.(*PackedVariantViewType)
		return ok && SameType(ta.Enum, tb.Enum) && ta.Variant != nil && tb.Variant != nil && ta.Variant.Name == tb.Variant.Name
	case *TreeVariantViewType:
		tb, ok := b.(*TreeVariantViewType)
		return ok && SameType(ta.Category, tb.Category) && ta.Variant != nil && tb.Variant != nil && ta.Variant.Name == tb.Variant.Name
	case *TreeNodeType:
		tb, ok := b.(*TreeNodeType)
		return ok && ta.Name == tb.Name
	case *TreeType:
		tb, ok := b.(*TreeType)
		return ok && ta.Name == tb.Name
	case *TreeCategoryType:
		tb, ok := b.(*TreeCategoryType)
		return ok && ta.Name == tb.Name
	case *TreeBlockType:
		tb, ok := b.(*TreeBlockType)
		return ok && ta.Name == tb.Name
	case *TreeStructType:
		tb, ok := b.(*TreeStructType)
		return ok && ta.Name == tb.Name
	case *EnumType:
		tb, ok := b.(*EnumType)
		return ok && ta.Name == tb.Name
	case *StructType:
		tb, ok := b.(*StructType)
		return ok && ta.Name == tb.Name
	case *OpaqueType:
		tb, ok := b.(*OpaqueType)
		return ok && ta.Name == tb.Name
	case *GenericInstanceType:
		tb, ok := b.(*GenericInstanceType)
		if !ok || ta.Name != tb.Name || len(ta.Args) != len(tb.Args) {
			return false
		}
		for i := range ta.Args {
			if !SameType(ta.Args[i], tb.Args[i]) {
				return false
			}
		}
		return SameType(ta.Base, tb.Base)
	case *AggregateStateType:
		tb, ok := b.(*AggregateStateType)
		return ok && sameAggregateStateLists(aggregateStateStates(ta), aggregateStateStates(tb)) && SameType(ta.Base, tb.Base)
	case *FuncType:
		tb, ok := b.(*FuncType)
		if !ok || ta.Variadic != tb.Variadic || len(ta.GenericParams) != len(tb.GenericParams) || len(ta.TypeParams) != len(tb.TypeParams) || len(ta.RefStorageParams) != len(tb.RefStorageParams) || len(ta.RefStateParams) != len(tb.RefStateParams) || len(ta.RegionParams) != len(tb.RegionParams) || len(ta.PermissionParams) != len(tb.PermissionParams) || len(ta.UsedPermissionParams) != len(tb.UsedPermissionParams) || len(ta.Permissions) != len(tb.Permissions) || len(ta.ShapeParams) != len(tb.ShapeParams) || len(ta.FreshReturnShapeParams) != len(tb.FreshReturnShapeParams) || len(ta.Params) != len(tb.Params) || !SameType(ta.Return, tb.Return) {
			return false
		}
		for i := range ta.GenericParams {
			if !sameGenericParam(ta.GenericParams[i], tb.GenericParams[i]) {
				return false
			}
		}
		for i := range ta.TypeParams {
			if ta.TypeParams[i] != tb.TypeParams[i] {
				return false
			}
		}
		for i := range ta.RefStorageParams {
			if ta.RefStorageParams[i] != tb.RefStorageParams[i] {
				return false
			}
		}
		for i := range ta.RefStateParams {
			if ta.RefStateParams[i] != tb.RefStateParams[i] {
				return false
			}
		}
		for i := range ta.RegionParams {
			if ta.RegionParams[i] != tb.RegionParams[i] {
				return false
			}
		}
		for i := range ta.PermissionParams {
			if ta.PermissionParams[i] != tb.PermissionParams[i] {
				return false
			}
		}
		for i := range ta.UsedPermissionParams {
			if ta.UsedPermissionParams[i] != tb.UsedPermissionParams[i] {
				return false
			}
		}
		for i := range ta.Permissions {
			if ta.Permissions[i] != tb.Permissions[i] {
				return false
			}
		}
		for i := range ta.ShapeParams {
			if ta.ShapeParams[i] != tb.ShapeParams[i] {
				return false
			}
		}
		for i := range ta.FreshReturnShapeParams {
			if ta.FreshReturnShapeParams[i] != tb.FreshReturnShapeParams[i] {
				return false
			}
		}
		for i := range ta.Params {
			if !SameType(ta.Params[i], tb.Params[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func AssignableTo(dst, src Type) bool {
	if dst == nil || src == nil {
		return false
	}
	if IsInvalidType(dst) || IsInvalidType(src) {
		return true
	}
	if IsNeverType(src) {
		return true
	}
	if assignableRuntimeCompatible(dst, src) {
		return true
	}
	if matchTypePattern(dst, src) {
		return true
	}
	if _, ok := dst.(*TypeParamType); ok {
		return true
	}
	if _, ok := src.(*TypeParamType); ok {
		return true
	}
	if _, ok := dst.(*AssociatedTypeProjection); ok {
		return SameType(dst, src)
	}
	if _, ok := src.(*AssociatedTypeProjection); ok {
		return SameType(dst, src)
	}
	if _, ok := dst.(*StructStateCaseType); ok {
		return namedStateTypeAssignable(dst, src)
	}
	if _, ok := dst.(*StructStateSetType); ok {
		return namedStateTypeAssignable(dst, src)
	}
	if _, ok := src.(*StructStateCaseType); ok {
		return SameType(dst, src)
	}
	if _, ok := src.(*StructStateSetType); ok {
		return SameType(dst, src)
	}
	if _, ok := dst.(*RefStorageParamType); ok {
		_, ok = src.(*RefStorageValueType)
		return ok
	}
	if _, ok := src.(*RefStorageParamType); ok {
		return true
	}
	if _, ok := dst.(*RefStateParamType); ok {
		_, ok = src.(*RefStateValueType)
		return ok
	}
	if _, ok := src.(*RefStateParamType); ok {
		return true
	}
	if _, ok := dst.(*RefStorageValueType); ok {
		return SameType(dst, src)
	}
	if _, ok := dst.(*RefStateValueType); ok {
		return SameType(dst, src)
	}
	if SameType(dst, src) {
		return true
	}
	if dstErr, ok := dst.(*ErrorSetType); ok {
		if srcErr, ok := src.(*ErrorSetType); ok {
			return ErrorSetAssignable(dstErr, srcErr)
		}
	}
	if du, ok := dst.(*ErrorUnionType); ok {
		if su, ok := src.(*ErrorUnionType); ok {
			return ErrorSetAssignable(du.Errors, su.Errors) && AssignableTo(du.Value, su.Value)
		}
		if AssignableTo(du.Value, src) {
			return true
		}
		if srcErr, ok := src.(*ErrorSetType); ok {
			return ErrorSetAssignable(du.Errors, srcErr)
		}
		return false
	}
	if dstOpt, ok := dst.(*OptionalType); ok {
		if srcOpt, ok := src.(*OptionalType); ok {
			return AssignableTo(dstOpt.Value, srcOpt.Value)
		}
		if IsNullType(src) {
			return true
		}
		return AssignableTo(dstOpt.Value, src)
	}
	if dstAgg, ok := dst.(*AggregateStateType); ok {
		srcAgg, ok := src.(*AggregateStateType)
		if !ok {
			return false
		}
		return SameType(dstAgg.Base, srcAgg.Base) && aggregateStateListAssignable(aggregateStateStates(dstAgg), aggregateStateStates(srcAgg))
	}
	if IsNumericType(dst) && IsNumericType(src) {
		return true
	}
	if IsNullType(src) {
		if r, ok := dst.(*RefType); ok {
			return r.State != RefStateNonNull
		}
		if _, ok := dst.(*OptionalType); ok {
			return true
		}
		return false
	}
	if dr, ok := dst.(*RefType); ok {
		if sr, ok := src.(*RefType); ok {
			if !SameType(dr.Elem, sr.Elem) {
				return false
			}
			if dr.Mutable && !sr.Mutable && dr.State != RefStateNull && sr.State != RefStateNull {
				return false
			}
			if dr.StateParam != "" || sr.StateParam != "" {
				return dr.StateParam == sr.StateParam && dr.StorageParam == sr.StorageParam && refRegionAssignable(dr.Region, sr.Region)
			}
			if dr.StorageParam != "" || sr.StorageParam != "" {
				return dr.StateParam == sr.StateParam && dr.StorageParam == sr.StorageParam && refStateAssignable(dr.State, sr.State) && refRegionAssignable(dr.Region, sr.Region)
			}
			return refStateAssignable(dr.State, sr.State) && refStorageAssignable(dr.Storage, sr.Storage, dr.ExplicitStorage, sr.ExplicitStorage) && refRegionAssignable(dr.Region, sr.Region)
		}
	}
	if dstNode, ok := dst.(*TreeNodeType); ok && dstNode != nil {
		switch srcTree := StripAggregateStateType(src).(type) {
		case *TreeCategoryType:
			return srcTree.Family == dstNode.Family
		case *TreeVariantViewType:
			return srcTree.Category != nil && srcTree.Category.Family == dstNode.Family
		case *TreeBlockType:
			return srcTree.Family == dstNode.Family
		case *TreeStructType:
			return srcTree.Family == dstNode.Family
		}
	}
	return false
}

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
	if _, ok := pattern.(*RefStorageParamType); ok {
		_, ok = actual.(*RefStorageValueType)
		return ok
	}
	if _, ok := pattern.(*RefStateParamType); ok {
		_, ok = actual.(*RefStateValueType)
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
		if p.StateParam != "" || a.StateParam != "" {
			if p.StateParam != a.StateParam {
				return false
			}
		} else if !refStateAssignable(p.State, a.State) {
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
		if !ok || p.Variadic != a.Variadic || len(p.RegionParams) != len(a.RegionParams) || len(p.ShapeParams) != len(a.ShapeParams) || len(p.FreshReturnShapeParams) != len(a.FreshReturnShapeParams) || len(p.Params) != len(a.Params) {
			return false
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
			if !matchTypePattern(p.Params[i], a.Params[i]) {
				return false
			}
		}
		return matchTypePattern(p.Return, a.Return)
	default:
		return SameType(pattern, actual)
	}
}

func MergeTypes(a, b Type) Type {
	if IsNeverType(a) {
		return b
	}
	if IsNeverType(b) {
		return a
	}
	if SameType(a, b) {
		return a
	}
	if au, ok := a.(*ErrorUnionType); ok {
		if bu, ok := b.(*ErrorUnionType); ok {
			merged := MergeTypes(au.Value, bu.Value)
			if IsInvalidType(merged) {
				return invalidType
			}
			switch {
			case ErrorSetAssignable(au.Errors, bu.Errors):
				return &ErrorUnionType{Value: merged, Errors: au.Errors}
			case ErrorSetAssignable(bu.Errors, au.Errors):
				return &ErrorUnionType{Value: merged, Errors: bu.Errors}
			}
		}
	}
	if ao, ok := a.(*OptionalType); ok {
		if bo, ok := b.(*OptionalType); ok {
			merged := MergeTypes(ao.Value, bo.Value)
			if IsInvalidType(merged) {
				return invalidType
			}
			return &OptionalType{Value: merged}
		}
		merged := MergeTypes(ao.Value, b)
		if IsInvalidType(merged) {
			return invalidType
		}
		return &OptionalType{Value: merged}
	}
	if bo, ok := b.(*OptionalType); ok {
		merged := MergeTypes(a, bo.Value)
		if IsInvalidType(merged) {
			return invalidType
		}
		return &OptionalType{Value: merged}
	}
	if _, ok := a.(*StructStateCaseType); ok {
		return mergeNamedStateTypes(a, b, nil)
	}
	if aset, ok := a.(*StructStateSetType); ok {
		return mergeNamedStateTypes(a, b, aset.Cases)
	}
	if _, ok := b.(*StructStateCaseType); ok {
		return mergeNamedStateTypes(a, b, nil)
	}
	if bset, ok := b.(*StructStateSetType); ok {
		return mergeNamedStateTypes(a, b, bset.Cases)
	}
	if aa, ok := a.(*AggregateStateType); ok {
		if ba, ok := b.(*AggregateStateType); ok && SameType(aa.Base, ba.Base) {
			if states, ok := mergeAggregateStateLists(aggregateStateStates(aa), aggregateStateStates(ba)); ok {
				return cloneAggregateStateWithBase(aa.Base, states)
			}
		}
	}
	if agi, ok := a.(*GenericInstanceType); ok {
		if bgi, ok := b.(*GenericInstanceType); ok && agi.Name == bgi.Name && SameType(agi.Base, bgi.Base) && len(agi.Args) == len(bgi.Args) {
			base, ok := agi.Base.(*StructType)
			if ok && base != nil {
				stateIndex := namedStateArgIndex(base)
				if stateIndex >= 0 {
					mergedArgs := make([]Type, len(agi.Args))
					for i := range agi.Args {
						if i == stateIndex {
							merged := mergeNamedStateTypes(agi.Args[i], bgi.Args[i], base.NamedStateCases)
							if IsInvalidType(merged) {
								return invalidType
							}
							mergedArgs[i] = merged
							continue
						}
						if !SameType(agi.Args[i], bgi.Args[i]) {
							return invalidType
						}
						mergedArgs[i] = agi.Args[i]
					}
					return &GenericInstanceType{Name: agi.Name, Base: agi.Base, Args: mergedArgs}
				}
			}
		}
	}
	if IsNumericType(a) && IsNumericType(b) {
		return CommonNumericType(a, b)
	}
	if ar, ok := a.(*RefType); ok {
		if br, ok := b.(*RefType); ok && SameType(ar.Elem, br.Elem) {
			mutable := ar.Mutable && br.Mutable
			if ar.StateParam != br.StateParam || ar.StorageParam != br.StorageParam {
				return invalidType
			}
			storage, explicit, okStorage := mergeRefStorages(ar.Storage, br.Storage, ar.ExplicitStorage, br.ExplicitStorage)
			if !okStorage {
				return invalidType
			}
			region, okRegion := mergeRefRegions(ar.Region, br.Region)
			if !okRegion {
				return invalidType
			}
			if state, ok := mergeRefStates(ar.State, br.State); ok {
				return &RefType{Elem: ar.Elem, Mutable: mutable, State: state, StateParam: ar.StateParam, Storage: storage, StorageParam: ar.StorageParam, Region: region, ExplicitStorage: explicit}
			}
		}
	}
	if av, ok := a.(*ViewType); ok {
		if bv, ok := b.(*ViewType); ok && SameType(av.Elem, bv.Elem) {
			if viewBoundsEqual(av, bv) {
				return av
			}
			return &ViewType{Elem: av.Elem}
		}
	}
	if IsNullType(a) {
		if opt, ok := b.(*OptionalType); ok {
			return opt
		}
		if r, ok := b.(*RefType); ok {
			switch r.State {
			case RefStateNull, RefStateNullable:
				return b
			case RefStateNonNull:
				return cloneRefTypeWithState(r, RefStateNullable)
			}
		}
	}
	if IsNullType(b) {
		if r, ok := a.(*RefType); ok {
			switch r.State {
			case RefStateNull, RefStateNullable:
				return a
			case RefStateNonNull:
				return cloneRefTypeWithState(r, RefStateNullable)
			}
		}
	}
	return invalidType
}
