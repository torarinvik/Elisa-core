package semantic

func (t *ConstEnumType) Member(name string) (*ConstEnumMember, bool) {
	if t == nil || t.MemberMap == nil {
		return nil, false
	}
	member, ok := t.MemberMap[name]
	return member, ok
}
func (t *EnumType) String() string   { return t.Name }
func (t *StructType) String() string { return t.Name }
func (t *OpaqueType) String() string { return t.Name }

func (t *TreeType) Member(name string) (Type, bool) {
	if t == nil || t.MemberTypes == nil {
		return nil, false
	}
	member, ok := t.MemberTypes[name]
	return member, ok
}

func (t *TreeCategoryType) Variant(name string) (*EnumVariant, bool) {
	if t == nil || t.VariantMap == nil {
		return nil, false
	}
	variant, ok := t.VariantMap[name]
	return variant, ok
}

func (t *TreeCategoryType) VariantViewType(variant *EnumVariant) *TreeVariantViewType {
	if t == nil || variant == nil {
		return nil
	}
	return &TreeVariantViewType{Category: t, Variant: variant}
}

func TreeKindType(t Type) (*ConstEnumType, bool) {
	switch tt := StripAggregateStateType(t).(type) {
	case *TreeCategoryType:
		if tt == nil || tt.KindType == nil {
			return nil, false
		}
		return tt.KindType, true
	case *TreeVariantViewType:
		if tt == nil || tt.Category == nil || tt.Category.KindType == nil {
			return nil, false
		}
		return tt.Category.KindType, true
	case *TreeNodeType:
		if tt == nil || tt.KindType == nil {
			return nil, false
		}
		return tt.KindType, true
	case *TreeBlockType:
		if tt == nil || tt.Family == nil || tt.Family.NodeType == nil || tt.Family.NodeType.KindType == nil {
			return nil, false
		}
		return tt.Family.NodeType.KindType, true
	case *TreeStructType:
		if tt == nil || tt.Family == nil || tt.Family.NodeType == nil || tt.Family.NodeType.KindType == nil {
			return nil, false
		}
		return tt.Family.NodeType.KindType, true
	default:
		return nil, false
	}
}

func TreeKindFieldInfo(t Type) (Field, bool) {
	kindType, ok := TreeKindType(t)
	if !ok || kindType == nil {
		return Field{}, false
	}
	return Field{Name: "kind", Type: kindType, Mutable: false}, true
}

func (t *TreeVariantViewType) Field(name string) (Field, bool) {
	if t == nil || t.Category == nil || t.Variant == nil {
		return Field{}, false
	}
	if field, ok := t.Category.Common[name]; ok {
		return field, true
	}
	index, ok := t.Variant.PayloadIndex(name)
	if ok && index >= 0 && index < len(t.Variant.Payload) {
		return Field{Name: name, Type: t.Variant.Payload[index], Mutable: false}, true
	}
	if name == "kind" {
		return TreeKindFieldInfo(t)
	}
	return Field{}, false
}

func (t *TreeVariantViewType) HasNamedPayloadFields() bool {
	if t == nil || t.Variant == nil || len(t.Variant.Payload) == 0 {
		return true
	}
	if len(t.Variant.PayloadNames) != len(t.Variant.Payload) {
		return false
	}
	for i := range t.Variant.Payload {
		if t.Variant.PayloadLabel(i) == "" {
			return false
		}
	}
	return true
}
