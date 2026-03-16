package semantic

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
	case *NullType:
		_, ok := b.(*NullType)
		return ok
	case *BuiltinType:
		tb, ok := b.(*BuiltinType)
		return ok && ta.Name == tb.Name
	case *TypeParamType:
		tb, ok := b.(*TypeParamType)
		return ok && ta.Name == tb.Name
	case *RefType:
		tb, ok := b.(*RefType)
		return ok && ta.State == tb.State && SameType(ta.Elem, tb.Elem)
	case *ArrayType:
		tb, ok := b.(*ArrayType)
		return ok && arraySizesEqual(ta, tb) && SameType(ta.Elem, tb.Elem)
	case *DArrayType:
		tb, ok := b.(*DArrayType)
		return ok && SameType(ta.Elem, tb.Elem) && SameShape(ta.Shape, tb.Shape)
	case *DArrayViewType:
		tb, ok := b.(*DArrayViewType)
		return ok && SameType(ta.Elem, tb.Elem)
	case *DListType:
		tb, ok := b.(*DListType)
		return ok && SameType(ta.Elem, tb.Elem) && SameShape(ta.Shape, tb.Shape)
	case *DListViewType:
		tb, ok := b.(*DListViewType)
		return ok && SameType(ta.Elem, tb.Elem)
	case *DStrType:
		tb, ok := b.(*DStrType)
		return ok && SameShape(ta.Shape, tb.Shape)
	case *SViewType:
		_, ok := b.(*SViewType)
		return ok
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
		if !ok || ta.Variadic != tb.Variadic || len(ta.TypeParams) != len(tb.TypeParams) || len(ta.ShapeParams) != len(tb.ShapeParams) || len(ta.FreshReturnShapeParams) != len(tb.FreshReturnShapeParams) || len(ta.Params) != len(tb.Params) || !SameType(ta.Return, tb.Return) {
			return false
		}
		for i := range ta.TypeParams {
			if ta.TypeParams[i] != tb.TypeParams[i] {
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
			return refStateAssignable(dr.State, sr.State)
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
	case *BuiltinType:
		a, ok := actual.(*BuiltinType)
		return ok && p.Name == a.Name
	case *NullType:
		_, ok := actual.(*NullType)
		return ok
	case *RefType:
		a, ok := actual.(*RefType)
		if !ok {
			return false
		}
		if !refStateAssignable(p.State, a.State) {
			return false
		}
		return matchTypePattern(p.Elem, a.Elem)
	case *ArrayType:
		a, ok := actual.(*ArrayType)
		return ok && arraySizesEqual(p, a) && matchTypePattern(p.Elem, a.Elem)
	case *DArrayType:
		a, ok := actual.(*DArrayType)
		return ok && matchTypePattern(p.Elem, a.Elem) && shapeMatchesPattern(p.Shape, a.Shape)
	case *DArrayViewType:
		a, ok := actual.(*DArrayViewType)
		return ok && matchTypePattern(p.Elem, a.Elem)
	case *DListType:
		a, ok := actual.(*DListType)
		return ok && matchTypePattern(p.Elem, a.Elem) && shapeMatchesPattern(p.Shape, a.Shape)
	case *DListViewType:
		a, ok := actual.(*DListViewType)
		return ok && matchTypePattern(p.Elem, a.Elem)
	case *DStrType:
		a, ok := actual.(*DStrType)
		return ok && shapeMatchesPattern(p.Shape, a.Shape)
	case *SViewType:
		_, ok := actual.(*SViewType)
		return ok
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
		if !ok || p.Variadic != a.Variadic || len(p.ShapeParams) != len(a.ShapeParams) || len(p.FreshReturnShapeParams) != len(a.FreshReturnShapeParams) || len(p.Params) != len(a.Params) {
			return false
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
	if SameType(a, b) {
		return a
	}
	if IsNumericType(a) && IsNumericType(b) {
		return CommonNumericType(a, b)
	}
	if ar, ok := a.(*RefType); ok {
		if br, ok := b.(*RefType); ok && SameType(ar.Elem, br.Elem) {
			if state, ok := mergeRefStates(ar.State, br.State); ok {
				return &RefType{Elem: ar.Elem, State: state}
			}
		}
	}
	if IsNullType(a) {
		if r, ok := b.(*RefType); ok {
			switch r.State {
			case RefStateNull, RefStateNullable:
				return b
			case RefStateNonNull:
				return &RefType{Elem: r.Elem, State: RefStateNullable}
			}
		}
	}
	if IsNullType(b) {
		if r, ok := a.(*RefType); ok {
			switch r.State {
			case RefStateNull, RefStateNullable:
				return a
			case RefStateNonNull:
				return &RefType{Elem: r.Elem, State: RefStateNullable}
			}
		}
	}
	return invalidType
}
