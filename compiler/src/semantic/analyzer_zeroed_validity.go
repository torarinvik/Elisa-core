package semantic

import "elisacore/src/ast"

// zeroedTypeHasInvalidRepresentation reports whether the all-zero bit pattern can
// violate a non-null representation invariant. It is deliberately stricter than
// definite-assignment tracking: an invalid value must not be materialized and then
// escaped through an alias before the later assignment can establish it.
func (a *Analyzer) zeroedTypeHasInvalidRepresentation(t Type) bool {
	return a.zeroedInvalidRepresentationCountAtDepth(t, 0) != 0
}

// zeroedInvalidRepresentationCount returns 0, 1, or 2 (meaning two or more)
// invalid leaves. The saturated count lets definite-assignment tracking permit
// a local placeholder with exactly one invalid leaf: one field write can then
// establish the whole invalid part of its representation. Multiple invalid
// leaves need path-sensitive initialization and must not be treated that way.
func (a *Analyzer) zeroedInvalidRepresentationCountAtDepth(t Type, depth int) int {
	if t == nil {
		return 0
	}
	if depth > semanticSubstitutionDepthLimit {
		// A malformed or excessively nested type cannot justify constructing a
		// value whose representation may contain a non-null reference.
		return 2
	}
	switch tt := t.(type) {
	case *RefType:
		if tt != nil && tt.State == RefStateNonNull {
			return 1
		}
	case *SViewType, *CStrType, *IDType, *TypeParamType:
		// String views and non-optional cstrs carry non-null pointers. An
		// unresolved type parameter may instantiate to any of those types.
		return 1
	case *OptionalType:
		// The zero representation is the valid absent case, regardless of the
		// optional's payload type.
		return 0
	case *AggregateStateType:
		return a.zeroedInvalidRepresentationCountAtDepth(tt.Base, depth+1)
	case *TupleType:
		count := 0
		for _, field := range tt.Fields {
			count += a.zeroedInvalidRepresentationCountAtDepth(field.Type, depth+1)
			if count > 1 {
				return 2
			}
		}
		return count
	case *ArrayType:
		if tt.HasConstSize && tt.ConstSize == 0 {
			return 0
		}
		elemCount := a.zeroedInvalidRepresentationCountAtDepth(tt.Elem, depth+1)
		if elemCount > 1 || !tt.HasConstSize || tt.ConstSize > 1 {
			if elemCount != 0 {
				return 2
			}
		}
		return elemCount
	case *StructType:
		count := 0
		for _, field := range tt.Fields {
			if field.Ghost || field.Phantom {
				continue
			}
			count += a.zeroedInvalidRepresentationCountAtDepth(field.Type, depth+1)
			if count > 1 {
				return 2
			}
		}
		return count
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if !ok || base == nil {
			// An unresolved or non-struct generic instance has no available layout
			// proof. Do not infer that its zero representation is valid from its
			// generic name or arguments; callers that need such a value must provide
			// a resolved representation or an explicit unsafe storage boundary.
			return 2
		}
		bindings := make(map[string]Type, len(base.TypeParams))
		for index, name := range base.TypeParams {
			if index < len(tt.Args) {
				bindings[name] = tt.Args[index]
			}
		}
		count := 0
		for _, field := range base.Fields {
			if field.Ghost || field.Phantom {
				continue
			}
			fieldType := a.substituteType(field.Type, bindings, map[string]Shape{}, map[string]string{}, map[string][]ast.PermissionRef{})
			count += a.zeroedInvalidRepresentationCountAtDepth(fieldType, depth+1)
			if count > 1 {
				return 2
			}
		}
		return count
	case *EnumType:
		// A zero tag is valid only when it names a declared variant, and every
		// active payload field of that variant must itself have a valid zero.
		for _, variant := range tt.Variants {
			if variant.Tag != 0 {
				continue
			}
			count := 0
			for _, payload := range variant.Payload {
				count += a.zeroedInvalidRepresentationCountAtDepth(payload, depth+1)
				if count > 1 {
					return 2
				}
			}
			return count
		}
		return 2
	}
	return 0
}

// zeroedTypeContainsSView is separate from the broader invalid-representation
// walk because an sview is never a placeholder: even a local that is assigned
// later must not temporarily hold a view with a null backing pointer.
func (a *Analyzer) zeroedTypeContainsSView(t Type) bool {
	return a.zeroedTypeContainsSViewAtDepth(t, 0)
}

func (a *Analyzer) zeroedTypeContainsSViewAtDepth(t Type, depth int) bool {
	if t == nil {
		return false
	}
	if depth > semanticSubstitutionDepthLimit {
		return true
	}
	switch tt := t.(type) {
	case *SViewType:
		return true
	case *OptionalType:
		return false
	case *AggregateStateType:
		return a.zeroedTypeContainsSViewAtDepth(tt.Base, depth+1)
	case *TupleType:
		for _, field := range tt.Fields {
			if a.zeroedTypeContainsSViewAtDepth(field.Type, depth+1) {
				return true
			}
		}
	case *ArrayType:
		if tt.HasConstSize && tt.ConstSize == 0 {
			return false
		}
		return a.zeroedTypeContainsSViewAtDepth(tt.Elem, depth+1)
	case *StructType:
		for _, field := range tt.Fields {
			if !field.Ghost && !field.Phantom && a.zeroedTypeContainsSViewAtDepth(field.Type, depth+1) {
				return true
			}
		}
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if !ok || base == nil {
			// As above, an unknown generic layout cannot prove that zero contains
			// no sview backing-pointer invariant.
			return true
		}
		bindings := make(map[string]Type, len(base.TypeParams))
		for index, name := range base.TypeParams {
			if index < len(tt.Args) {
				bindings[name] = tt.Args[index]
			}
		}
		for _, field := range base.Fields {
			if field.Ghost || field.Phantom {
				continue
			}
			fieldType := a.substituteType(field.Type, bindings, map[string]Shape{}, map[string]string{}, map[string][]ast.PermissionRef{})
			if a.zeroedTypeContainsSViewAtDepth(fieldType, depth+1) {
				return true
			}
		}
	case *EnumType:
		for _, variant := range tt.Variants {
			if variant.Tag != 0 {
				continue
			}
			for _, payload := range variant.Payload {
				if a.zeroedTypeContainsSViewAtDepth(payload, depth+1) {
					return true
				}
			}
			return false
		}
		return true
	}
	return false
}
