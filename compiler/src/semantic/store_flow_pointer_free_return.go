package semantic

// A copied scalar aggregate cannot carry argument storage through a call's result.
// Use resolved types so qualified names and aliases have the same meaning. This
// is deliberately narrower than borrow-free contents: containers and packed enum
// handles can still refer to backing storage even when their elements are scalar.
func storeFlowPointerFreeReturn(t Type, active map[Type]bool) bool {
	if t == nil || active[t] {
		return false
	}
	active[t] = true
	defer delete(active, t)
	switch tt := t.(type) {
	case *BuiltinType:
		return tt != nil && storeFlowScalarTypeNames[tt.Name]
	case *BitIntType:
		return tt != nil
	case *ConstEnumType:
		return tt != nil
	case *BitGroupType:
		return tt != nil
	case *OptionalType:
		return tt != nil && storeFlowPointerFreeReturn(tt.Value, active)
	case *TupleType:
		if tt == nil {
			return false
		}
		for _, field := range tt.Fields {
			if !storeFlowPointerFreeReturn(field.Type, active) {
				return false
			}
		}
		return true
	case *StructType:
		if tt == nil || tt.Fields == nil || tt.Resource || len(tt.TypeParams) != 0 || len(tt.GenericParams) != 0 {
			return false
		}
		for _, field := range tt.Fields {
			if field.IsTail || !storeFlowPointerFreeReturn(field.Type, active) {
				return false
			}
		}
		return true
	case *EnumType:
		if tt == nil || tt.Variants == nil || tt.Packed || tt.StoreBackedPlain || tt.StoreType != nil {
			return false
		}
		for _, field := range tt.Common {
			if !storeFlowPointerFreeReturn(field.Type, active) {
				return false
			}
		}
		for _, variant := range tt.Variants {
			if variant == nil {
				return false
			}
			for _, payload := range variant.Payload {
				if !storeFlowPointerFreeReturn(payload, active) {
					return false
				}
			}
		}
		return true
	}
	return false
}
