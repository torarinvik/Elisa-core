package semantic

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
	case *ErrorSetType:
		tb, ok := b.(*ErrorSetType)
		return ok && ErrorSetTagsEqual(ta, tb)
	case *ErrorUnionType:
		tb, ok := b.(*ErrorUnionType)
		return ok && SameType(ta.Value, tb.Value) && SameType(ta.Errors, tb.Errors)
	case *ConstEnumType:
		tb, ok := b.(*ConstEnumType)
		return ok && ta.Name == tb.Name
	case *RefType:
		tb, ok := b.(*RefType)
		return ok && ta.State == tb.State && ta.Storage == tb.Storage && ta.Region == tb.Region && SameType(ta.Elem, tb.Elem)
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
	case *FuncType:
		tb, ok := b.(*FuncType)
		if !ok || ta.Variadic != tb.Variadic || len(ta.TypeParams) != len(tb.TypeParams) || len(ta.RegionParams) != len(tb.RegionParams) || len(ta.PermissionParams) != len(tb.PermissionParams) || len(ta.UsedPermissionParams) != len(tb.UsedPermissionParams) || len(ta.Permissions) != len(tb.Permissions) || len(ta.ShapeParams) != len(tb.ShapeParams) || len(ta.FreshReturnShapeParams) != len(tb.FreshReturnShapeParams) || len(ta.Params) != len(tb.Params) || !SameType(ta.Return, tb.Return) {
			return false
		}
		for i := range ta.TypeParams {
			if ta.TypeParams[i] != tb.TypeParams[i] {
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
	if IsNumericType(dst) && IsNumericType(src) {
		return true
	}
	if IsNullType(src) {
		if r, ok := dst.(*RefType); ok {
			return r.State != RefStateNonNull
		}
		return false
	}
	if dr, ok := dst.(*RefType); ok {
		if sr, ok := src.(*RefType); ok {
			if !SameType(dr.Elem, sr.Elem) {
				return false
			}
			return refStateAssignable(dr.State, sr.State) && refStorageAssignable(dr.Storage, sr.Storage, dr.ExplicitStorage, sr.ExplicitStorage) && refRegionAssignable(dr.Region, sr.Region)
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
	switch p := pattern.(type) {
	case *NeverType:
		_, ok := actual.(*NeverType)
		return ok
	case *BuiltinType:
		a, ok := actual.(*BuiltinType)
		return ok && p.Name == a.Name
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
	case *ConstEnumType:
		a, ok := actual.(*ConstEnumType)
		return ok && p.Name == a.Name
	case *RefType:
		a, ok := actual.(*RefType)
		if !ok {
			return false
		}
		if !refStateAssignable(p.State, a.State) {
			return false
		}
		if !refStorageAssignable(p.Storage, a.Storage, p.ExplicitStorage, a.ExplicitStorage) {
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
	if IsNumericType(a) && IsNumericType(b) {
		return CommonNumericType(a, b)
	}
	if ar, ok := a.(*RefType); ok {
		if br, ok := b.(*RefType); ok && SameType(ar.Elem, br.Elem) {
			storage, explicit, okStorage := mergeRefStorages(ar.Storage, br.Storage, ar.ExplicitStorage, br.ExplicitStorage)
			if !okStorage {
				return invalidType
			}
			region, okRegion := mergeRefRegions(ar.Region, br.Region)
			if !okRegion {
				return invalidType
			}
			if state, ok := mergeRefStates(ar.State, br.State); ok {
				return &RefType{Elem: ar.Elem, State: state, Storage: storage, Region: region, ExplicitStorage: explicit}
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
