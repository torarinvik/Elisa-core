package semantic

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
	if dstEnum, ok := dst.(*EnumType); ok {
		if srcView, ok := src.(*PackedVariantViewType); ok && srcView.Enum != nil {
			return SameType(dstEnum, srcView.Enum)
		}
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
	if _, ok := dst.(*IDType); ok {
		return SameType(dst, src)
	}
	if _, ok := src.(*IDType); ok {
		return SameType(dst, src)
	}
	if _, ok := dst.(*AddressSpaceType); ok {
		return SameType(dst, src)
	}
	if _, ok := src.(*AddressSpaceType); ok {
		return SameType(dst, src)
	}
	if dstTuple, ok := dst.(*TupleType); ok {
		srcTuple, ok := src.(*TupleType)
		if !ok || len(dstTuple.Fields) != len(srcTuple.Fields) {
			return false
		}
		for i := range dstTuple.Fields {
			if !AssignableTo(dstTuple.Fields[i].Type, srcTuple.Fields[i].Type) {
				return false
			}
		}
		return true
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
	if sr, ok := src.(*RefType); ok && sr != nil && (IsNumericType(sr.Elem) || IsBoolType(sr.Elem)) && SameType(dst, sr.Elem) {
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
	if dstCategory, ok := dst.(*TreeCategoryType); ok && dstCategory != nil {
		if srcCategory, ok := StripAggregateStateType(src).(*TreeCategoryType); ok && treeCategoryDescendsFrom(srcCategory, dstCategory) {
			return true
		}
		if srcView, ok := StripAggregateStateType(src).(*TreeVariantViewType); ok && srcView.Category != nil && treeCategoryDescendsFrom(srcView.Category, dstCategory) {
			return true
		}
	}
	return false
}

func treeCategoryDescendsFrom(src *TreeCategoryType, dst *TreeCategoryType) bool {
	for current := src; current != nil; current = current.Parent {
		if SameType(current, dst) {
			return true
		}
	}
	return false
}
