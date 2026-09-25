package semantic

import "elisacore/src/ast"

// zeroedTypeHasInvalidRepresentation reports whether the all-zero bit pattern
// can violate a runtime representation invariant. It is deliberately stricter
// than definite-assignment tracking: an invalid value must not be materialized
// and escaped through an alias before a later assignment establishes it.
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
		return 2
	}
	if depth > semanticSubstitutionDepthLimit {
		// A malformed or excessively nested type cannot justify constructing a
		// value whose representation may contain a non-null reference.
		return 2
	}
	switch tt := t.(type) {
	case *InvalidType, *NeverType:
		// These are analysis-only or non-materializable types.
		return 2
	case *NullType:
		// `null` is itself the valid zero value of the null literal type.
		return 0
	case *BuiltinType:
		if tt == nil || !builtinZeroRepresentationIsValid(tt.Name) {
			return 2
		}
		return 0
	case *BitIntType:
		if tt != nil && tt.Width > 0 && tt.Width <= 64 {
			return 0
		}
		return 2
	case *RefType:
		if tt == nil {
			return 2
		}
		if tt.State == RefStateNonNull {
			return 1
		}
		return 0
	case *SViewType, *CStrType, *IDType:
		// These carry a non-null backing pointer or a live-object handle.
		return 1
	case *TypeParamType:
		if tt == nil {
			return 2
		}
		// One unresolved payload is one potentially invalid leaf. Repeated or
		// aggregate occurrences saturate to 2 in the enclosing representation.
		return 1
	case *AddressSpaceType, *ConstParamType, *ConstValueType,
		*StructStateCaseType, *StructStateSetType, *RefStorageValueType,
		*RegionParamType, *RegionValueType, *OpaqueType, *FuncType,
		*StoreRowsViewType, *StoreRowViewType, *DictEntryType,
		*PackedEnumStoreType, *PackedVariantViewType:
		// These pointer-like, analysis-only, or opaque forms need a dedicated
		// representation proof. Unknown layout is not evidence of valid zero.
		return 2
	case *OptionalType:
		if tt == nil || tt.Value == nil {
			return 2
		}
		// The zero representation is the valid absent case, regardless of the
		// optional's payload type.
		return 0
	case *AggregateStateType:
		if tt == nil || tt.Base == nil {
			return 2
		}
		return a.zeroedInvalidRepresentationCountAtDepth(tt.Base, depth+1)
	case *TupleType:
		if tt == nil {
			return 2
		}
		count := 0
		for _, field := range tt.Fields {
			count += a.zeroedInvalidRepresentationCountAtDepth(field.Type, depth+1)
			if count > 1 {
				return 2
			}
		}
		return count
	case *BitGroupType:
		if tt == nil || len(tt.Members) == 0 {
			return 2
		}
		count := 0
		for _, member := range tt.Members {
			if member.Width <= 0 || member.Type == nil {
				return 2
			}
			count += a.zeroedInvalidRepresentationCountAtDepth(member.Type, depth+1)
			if count > 1 {
				return 2
			}
		}
		return count
	case *ConstEnumType:
		if tt == nil || tt.Storage == nil {
			return 2
		}
		storageCount := a.zeroedInvalidRepresentationCountAtDepth(tt.Storage, depth+1)
		for _, member := range tt.Members {
			if member != nil && member.Value == 0 && storageCount == 0 {
				return 0
			}
		}
		return 2
	case *ErrorSetType:
		// Code zero is the no-error/success state. Error payload slots are
		// inactive in this representation.
		if tt != nil {
			return 0
		}
		return 2
	case *ErrorUnionType:
		if tt == nil || tt.Value == nil || tt.Errors == nil {
			return 2
		}
		if isVoidType(tt.Value) {
			return 0
		}
		// A value-carrying error union stores its success payload indirectly.
		// The all-zero descriptor has a null payload pointer even though code 0
		// selects success, so it is not a valid value.
		return 1
	case *ArrayType:
		if tt == nil || tt.Elem == nil {
			return 2
		}
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
		if tt == nil {
			return 2
		}
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
	case *DArrayType:
		if tt == nil {
			return 2
		}
		return 0
	case *ViewType:
		if tt == nil {
			return 2
		}
		return 0
	case *DictType:
		if tt == nil {
			return 2
		}
		return 0
	case *SetType:
		if tt == nil {
			return 2
		}
		// These runtime headers have a valid empty zero representation: null
		// backing storage plus zero length/count/capacity. Their elements are not
		// stored inline until a value is inserted.
		return 0
	case *GenericInstanceType:
		if tt == nil {
			return 2
		}
		base, ok := tt.Base.(*StructType)
		if !ok || base == nil {
			// An unresolved or non-struct generic instance has no available layout
			// proof. Do not infer that its zero representation is valid from its
			// generic name or arguments.
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
		if tt == nil {
			return 2
		}
		// A zero tag is valid only when it names a declared variant, and every
		// active payload field of that variant must itself have a valid zero.
		for _, variant := range tt.Variants {
			if variant == nil {
				return 2
			}
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
	default:
		// Type is an open Go interface within this package. A newly introduced
		// semantic representation must be classified explicitly above; absence
		// from this switch is not evidence that zero is valid.
		return 2
	}
}

func builtinZeroRepresentationIsValid(name string) bool {
	switch name {
	case "bool", "char", "int", "i8", "i16", "i32", "i64", "isize",
		"u8", "u16", "u32", "u64", "usize", "uintptr", "f32", "f64":
		return true
	default:
		return false
	}
}

func builtinCanContainSView(name string) bool {
	if name == "void" || builtinZeroRepresentationIsValid(name) {
		return false
	}
	switch name {
	case "Local", "Frozen", "Joinable", "Pending", "Held":
		return false
	default:
		return true
	}
}

// zeroedTypeContainsSView is separate from the broader invalid-representation
// walk because an sview is never a placeholder: even a local that is assigned
// later must not temporarily hold a view with a null backing pointer.
func (a *Analyzer) zeroedTypeContainsSView(t Type) bool {
	return a.zeroedTypeContainsSViewAtDepth(t, 0)
}

func (a *Analyzer) zeroedTypeContainsSViewAtDepth(t Type, depth int) bool {
	if t == nil {
		return true
	}
	if depth > semanticSubstitutionDepthLimit {
		return true
	}
	switch tt := t.(type) {
	case *SViewType:
		return true
	case *BuiltinType:
		return tt == nil || builtinCanContainSView(tt.Name)
	case *BitIntType:
		return tt == nil || tt.Width <= 0 || tt.Width > 64
	case *OptionalType:
		if tt == nil || tt.Value == nil {
			return true
		}
		// Zero is the absent representation; the optional payload is inactive.
		return false
	case *AggregateStateType:
		return tt == nil || tt.Base == nil || a.zeroedTypeContainsSViewAtDepth(tt.Base, depth+1)
	case *TupleType:
		if tt == nil {
			return true
		}
		for _, field := range tt.Fields {
			if a.zeroedTypeContainsSViewAtDepth(field.Type, depth+1) {
				return true
			}
		}
		return false
	case *BitGroupType:
		if tt == nil {
			return true
		}
		for _, member := range tt.Members {
			if member.Type == nil || a.zeroedTypeContainsSViewAtDepth(member.Type, depth+1) {
				return true
			}
		}
		return false
	case *ArrayType:
		if tt == nil || tt.Elem == nil {
			return true
		}
		if tt.HasConstSize && tt.ConstSize == 0 {
			return false
		}
		return a.zeroedTypeContainsSViewAtDepth(tt.Elem, depth+1)
	case *StructType:
		if tt == nil {
			return true
		}
		for _, field := range tt.Fields {
			if !field.Ghost && !field.Phantom && a.zeroedTypeContainsSViewAtDepth(field.Type, depth+1) {
				return true
			}
		}
		return false
	case *GenericInstanceType:
		if tt == nil {
			return true
		}
		base, ok := tt.Base.(*StructType)
		if !ok || base == nil {
			// An unknown generic layout cannot prove that zero contains no sview
			// backing-pointer invariant.
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
		return false
	case *EnumType:
		if tt == nil {
			return true
		}
		for _, variant := range tt.Variants {
			if variant == nil {
				return true
			}
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
	case *DArrayType:
		return tt == nil
	case *ViewType:
		return tt == nil
	case *DictType:
		return tt == nil
	case *SetType:
		return tt == nil
	case *RefType:
		return tt == nil
	case *CStrType:
		return tt == nil
	case *IDType:
		return tt == nil
	case *TypeParamType:
		return tt == nil
	case *AddressSpaceType, *ConstParamType, *ConstValueType,
		*StructStateCaseType, *StructStateSetType,
		*RefStorageValueType, *RegionParamType, *RegionValueType:
		// These forms contain no inline sview value in their zero representation.
		// Their other validity obligations are handled by the representation walk.
		return false
	case *ConstEnumType:
		return tt == nil
	case *ErrorSetType:
		if tt == nil {
			return true
		}
		// Code zero is the no-error state; no error payload is active.
		return false
	case *ErrorUnionType:
		return tt == nil || tt.Value == nil || tt.Errors == nil
	case *FuncType:
		return false
	case *InvalidType, *NeverType, *NullType:
		return false
	case *OpaqueType:
		// Extern opaque values lower to opaque pointers. They cannot contain an
		// inline sview, even though their nullability and zero-value validity
		// still require a separate proof in zeroedInvalidRepresentationCount.
		return false
	case *StoreRowsViewType, *StoreRowViewType, *DictEntryType,
		*PackedEnumStoreType, *PackedVariantViewType:
		return true
	default:
		// Unknown semantic representations are not evidence that zero is free of
		// the sview non-null backing-pointer invariant.
		return true
	}
}
