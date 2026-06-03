package semantic

func cloneRefTypeWithState(ref *RefType, state RefState) *RefType {
	if ref == nil {
		return nil
	}
	return &RefType{Elem: ref.Elem, Mutable: ref.Mutable, State: state, Storage: ref.Storage, Region: ref.Region, ExplicitStorage: ref.ExplicitStorage}
}

func cloneRefType(ref *RefType) *RefType {
	if ref == nil {
		return nil
	}
	return cloneRefTypeWithState(ref, ref.State)
}

func refStateAssignable(dst, src RefState) bool {
	switch dst {
	case RefStateNullable:
		return true
	case RefStateNonNull:
		return src == RefStateNonNull
	case RefStateNull:
		return src == RefStateNull
	default:
		return false
	}
}

func refStorageAssignable(dstStorage, srcStorage RefStorage, dstExplicit, srcExplicit bool) bool {
	if !dstExplicit || !srcExplicit {
		return true
	}
	return dstStorage == srcStorage
}

func refRegionAssignable(dstRegion, srcRegion string) bool {
	if dstRegion == "" {
		return true
	}
	return dstRegion == srcRegion
}

func mergeRefRegions(a, b string) (string, bool) {
	if a == b {
		return a, true
	}
	if a == "" || b == "" {
		return "", true
	}
	return "", false
}

func mergeRefStorages(aStorage, bStorage RefStorage, aExplicit, bExplicit bool) (RefStorage, bool, bool) {
	if !aExplicit || !bExplicit {
		return RefStorageAny, false, true
	}
	if aStorage == bStorage {
		return aStorage, true, true
	}
	return RefStorageAny, false, false
}

func mergeRefStates(a, b RefState) (RefState, bool) {
	if a == b {
		return a, true
	}
	if a == RefStateNullable || b == RefStateNullable {
		return RefStateNullable, true
	}
	if (a == RefStateNonNull && b == RefStateNull) || (a == RefStateNull && b == RefStateNonNull) {
		return RefStateNullable, true
	}
	return RefStateNullable, false
}

func CommonNumericType(a, b Type) Type {
	if !IsNumericType(a) || !IsNumericType(b) {
		return invalidType
	}
	if SameType(a, b) {
		return a
	}
	if IsFloatType(a) || IsFloatType(b) {
		if ta, ok := a.(*BuiltinType); ok && ta.Name == "f64" {
			return a
		}
		if tb, ok := b.(*BuiltinType); ok && tb.Name == "f64" {
			return b
		}
		if ta, ok := a.(*BuiltinType); ok && ta.Name == "f32" {
			return a
		}
		if tb, ok := b.(*BuiltinType); ok && tb.Name == "f32" {
			return b
		}
	}
	if ta, ok := a.(*BuiltinType); ok && ta.Name == "char" {
		return b
	}
	if tb, ok := b.(*BuiltinType); ok && tb.Name == "char" {
		return a
	}
	if ta, ok := a.(*BuiltinType); ok && ta.Name == "int" {
		return b
	}
	if tb, ok := b.(*BuiltinType); ok && tb.Name == "int" {
		return a
	}
	return a
}

func arraySizesEqual(a, b *ArrayType) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.HasConstSize && b.HasConstSize {
		return a.ConstSize == b.ConstSize
	}
	return a.Size == b.Size
}

func viewBoundsEqual(a, b *ViewType) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Begin == b.Begin && a.End == b.End
}

func viewBoundsMatch(pattern, actual *ViewType) bool {
	if pattern == nil || actual == nil {
		return pattern == actual
	}
	if pattern.Begin == "" && pattern.End == "" {
		return true
	}
	return viewBoundsEqual(pattern, actual)
}

func SameShape(a, b Shape) bool {
	if a == nil || b == nil {
		return a == b
	}
	switch sa := a.(type) {
	case *ShapeParam:
		sb, ok := b.(*ShapeParam)
		return ok && sa.Name == sb.Name
	case *NamedShape:
		sb, ok := b.(*NamedShape)
		return ok && sa.Name == sb.Name
	case *WildcardShape:
		_, ok := b.(*WildcardShape)
		return ok
	case *FreshShape:
		sb, ok := b.(*FreshShape)
		return ok && sa.ID == sb.ID
	default:
		return false
	}
}

func shapeMatchesPattern(pattern, actual Shape) bool {
	if pattern == nil || actual == nil {
		return pattern == actual
	}
	if isWildcardShape(pattern) {
		return true
	}
	return SameShape(pattern, actual)
}

func isWildcardShape(shape Shape) bool {
	_, ok := shape.(*WildcardShape)
	return ok
}
