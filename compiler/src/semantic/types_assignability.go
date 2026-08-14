package semantic

// funcAssignableIgnoringNarrowerPermissions reports whether `src` is a function type that
// differs from `dst` ONLY by requiring FEWER effects.
//
// A callback that needs a SUBSET of the permitted effects is safe wherever the wider type is
// expected: it simply never uses the rest. The reverse must stay rejected, since that would
// let an unaccounted effect happen. SameType requires the sets to be EQUAL, which made
// `fn(i64) -> i64 can[Abort]` un-passable to a parameter declared
// `fn(i64) -> i64 can[Abort, Memory]` — rejecting a sound program.
//
// Implemented by re-checking with src's permissions replaced by dst's, so EVERY other
// property still goes through SameType and this cannot loosen anything else.
func funcAssignableIgnoringNarrowerPermissions(dst, src Type) bool {
	dstFn, ok := dst.(*FuncType)
	if !ok || dstFn == nil {
		return false
	}
	srcFn, ok := src.(*FuncType)
	if !ok || srcFn == nil {
		return false
	}
	if len(srcFn.Permissions) >= len(dstFn.Permissions) {
		return false
	}
	permitted := make(map[string]bool, len(dstFn.Permissions))
	for _, permission := range dstFn.Permissions {
		permitted[permission] = true
	}
	for _, permission := range srcFn.Permissions {
		if !permitted[permission] {
			return false
		}
	}
	widened := *srcFn
	widened.Permissions = dstFn.Permissions
	return SameType(dst, &widened)
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
	if _, ok := dst.(*RefStorageValueType); ok {
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
			return refStateAssignable(dr.State, sr.State) && refStorageAssignable(dr.Storage, sr.Storage, dr.ExplicitStorage, sr.ExplicitStorage) && refRegionAssignable(dr.Region, sr.Region)
		}
	}
	// View mutability variance (mirrors RefType): a read-only `view[T]` may narrow from a
	// `mutable view[T]` (drop write capability, like `&mut [T]` → `&[T]`), but a `mutable view[T]`
	// may NOT be assigned from a read-only `view[T]` — that would launder write access onto a borrow
	// whose source is immutable. Element type must match; other fields (bounds/region/surface) are
	// inert, as in SameType.
	if dv, ok := dst.(*ViewType); ok {
		if sv, ok := src.(*ViewType); ok {
			if !SameType(dv.Elem, sv.Elem) {
				return false
			}
			return !(dv.Mutable && !sv.Mutable)
		}
	}
	// Sealed enum refinement (docs/77): a Child enum (a subset of cases) is assignable to its Parent
	// (the wider union) — `enum Child is Parent:` ⟹ Child <: Parent. Widening only; the reverse
	// requires an explicit narrowing match/`is` test. Unrelated enums never relate (enumDescendsFrom
	// returns false), so existing behavior is unchanged.
	if dstEnum, ok := dst.(*EnumType); ok && dstEnum != nil {
		if srcEnum, ok := StripAggregateStateType(src).(*EnumType); ok && srcEnum != dstEnum && enumDescendsFrom(srcEnum, dstEnum) {
			return true
		}
	}
	// A function value that needs FEWER effects than the position permits. Checked LAST, so
	// every other rule decides first and this only ever accepts what SameType would have
	// accepted but for the permission set.
	if funcAssignableIgnoringNarrowerPermissions(dst, src) {
		return true
	}
	return false
}
