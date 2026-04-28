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
	case *TupleType:
		tb, ok := b.(*TupleType)
		if !ok || len(ta.Fields) != len(tb.Fields) {
			return false
		}
		for i := range ta.Fields {
			if !SameType(ta.Fields[i].Type, tb.Fields[i].Type) {
				return false
			}
		}
		return true
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
	case *StoreRowsViewType:
		tb, ok := b.(*StoreRowsViewType)
		return ok && SameType(ta.Store, tb.Store)
	case *StoreRowViewType:
		tb, ok := b.(*StoreRowViewType)
		return ok && SameType(ta.Store, tb.Store)
	case *DStrType:
		tb, ok := b.(*DStrType)
		return ok && SameShape(ta.Shape, tb.Shape)
	case *DictType:
		tb, ok := b.(*DictType)
		return ok && SameType(ta.Key, tb.Key) && SameType(ta.Value, tb.Value)
	case *DictEntryType:
		tb, ok := b.(*DictEntryType)
		return ok && ta.Mutable == tb.Mutable && SameType(ta.Dict, tb.Dict)
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
		if !ok || ta.Variadic != tb.Variadic || funcTypeExplicitParamCount(ta) != funcTypeExplicitParamCount(tb) || len(ta.ExplicitParamNames) != len(tb.ExplicitParamNames) || len(ta.ImplicitParamNames) != len(tb.ImplicitParamNames) || len(ta.GenericParams) != len(tb.GenericParams) || len(ta.TypeParams) != len(tb.TypeParams) || len(ta.RefStorageParams) != len(tb.RefStorageParams) || len(ta.RefStateParams) != len(tb.RefStateParams) || len(ta.RegionParams) != len(tb.RegionParams) || len(ta.PermissionParams) != len(tb.PermissionParams) || len(ta.UsedPermissionParams) != len(tb.UsedPermissionParams) || len(ta.Permissions) != len(tb.Permissions) || len(ta.ShapeParams) != len(tb.ShapeParams) || len(ta.FreshReturnShapeParams) != len(tb.FreshReturnShapeParams) || len(ta.Params) != len(tb.Params) || !SameType(ta.Return, tb.Return) {
			return false
		}
		for i := range ta.ExplicitParamNames {
			if ta.ExplicitParamNames[i] != tb.ExplicitParamNames[i] {
				return false
			}
		}
		for i := range ta.ImplicitParamNames {
			if ta.ImplicitParamNames[i] != tb.ImplicitParamNames[i] {
				return false
			}
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
