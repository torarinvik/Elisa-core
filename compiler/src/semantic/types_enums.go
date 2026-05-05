package semantic

import (
	"elisacore/src/ast"
)

func (t *EnumType) Variant(name string) (*EnumVariant, bool) {
	if t == nil || t.VariantMap == nil {
		return nil, false
	}
	variant, ok := t.VariantMap[name]
	return variant, ok
}

func (t *EnumType) HasInlineWordHandleVariant() bool {
	if t == nil {
		return false
	}
	for _, variant := range t.Variants {
		if variant.CanInlineWordHandle(t) {
			return true
		}
	}
	return false
}

func (v *EnumVariant) HasNamedPayloads() bool {
	if v == nil || len(v.PayloadNames) == 0 {
		return false
	}
	return v.PayloadNames[0] != ""
}

func (v *EnumVariant) CanInlineWordHandle(enumType *EnumType) bool {
	if v == nil || enumType == nil || !enumType.Packed {
		return false
	}
	if len(enumType.Common) != 0 {
		return false
	}
	if v.Tag >= 1<<15 {
		return false
	}
	if _, hasTail := v.TailPayloadIndex(); hasTail {
		return false
	}
	switch len(v.Payload) {
	case 0:
		return true
	case 1:
		return inlineWordHandlePayloadType(v.Payload[0])
	default:
		return false
	}
}

func inlineWordHandlePayloadType(t Type) bool {
	if storage, ok := ConstEnumStorageType(t); ok {
		return inlineWordHandlePayloadType(storage)
	}
	builtin, ok := t.(*BuiltinType)
	if !ok || builtin == nil {
		return false
	}
	switch builtin.Name {
	case "bool", "i8", "i16", "i32", "u8", "u16", "u32":
		return true
	default:
		return false
	}
}

func (v *EnumVariant) PayloadIndex(name string) (int, bool) {
	if v == nil {
		return 0, false
	}
	for i, payloadName := range v.PayloadNames {
		if payloadName == name {
			return i, true
		}
	}
	return 0, false
}

func (v *EnumVariant) PayloadLabel(index int) string {
	if v == nil || index < 0 || index >= len(v.PayloadNames) {
		return ""
	}
	return v.PayloadNames[index]
}

func (v *EnumVariant) PayloadRelation(index int) ast.EnumPayloadRelation {
	if v == nil || index < 0 || index >= len(v.PayloadRelations) {
		return ast.EnumPayloadRelationNone
	}
	return v.PayloadRelations[index]
}

func (v *EnumVariant) HasStructuralPayloads() bool {
	if v == nil {
		return false
	}
	for i := range v.Payload {
		switch v.PayloadRelation(i) {
		case ast.EnumPayloadRelationChild, ast.EnumPayloadRelationChildren:
			return true
		}
	}
	return false
}

func (v *EnumVariant) PackedViewType(enumType *EnumType) *PackedVariantViewType {
	if v == nil || enumType == nil {
		return nil
	}
	if v.packedViewType != nil && v.packedViewType.Enum == enumType {
		return v.packedViewType
	}
	v.packedViewType = &PackedVariantViewType{Enum: enumType, Variant: v}
	return v.packedViewType
}

func (t *PackedVariantViewType) Field(name string) (Field, bool) {
	if t == nil || t.Enum == nil || t.Variant == nil {
		return Field{}, false
	}
	if field, ok := t.Enum.Common[name]; ok {
		return field, true
	}
	index, ok := t.Variant.PayloadIndex(name)
	if !ok || index < 0 || index >= len(t.Variant.Payload) {
		return Field{}, false
	}
	return Field{Name: name, Type: t.Variant.Payload[index], Mutable: false}, true
}

func (t *PackedVariantViewType) HasNamedPayloadFields() bool {
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

func (v *EnumVariant) HasTailPayload() bool {
	if v == nil {
		return false
	}
	return v.TailIndex >= 0 && v.TailIndex < len(v.Payload)
}

func (v *EnumVariant) TailPayloadIndex() (int, bool) {
	if !v.HasTailPayload() {
		return 0, false
	}
	return v.TailIndex, true
}

func (v *EnumVariant) TailPayloadViewType() (*DArrayViewType, bool) {
	index, ok := v.TailPayloadIndex()
	if !ok {
		return nil, false
	}
	viewType, ok := v.Payload[index].(*DArrayViewType)
	if !ok {
		return nil, false
	}
	return viewType, true
}

func (v *EnumVariant) TailPayloadElemType() (Type, bool) {
	viewType, ok := v.TailPayloadViewType()
	if !ok || viewType == nil {
		return nil, false
	}
	return viewType.Elem, true
}
