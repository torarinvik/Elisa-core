package semantic

import (
	"fmt"
	"strings"
)

func (*InvalidType) isType()           {}
func (*NeverType) isType()             {}
func (*NullType) isType()              {}
func (*BuiltinType) isType()           {}
func (*BitIntType) isType()            {}
func (*IDType) isType()                {}
func (*AddressSpaceType) isType()      {}
func (*TypeParamType) isType()         {}
func (*ConstParamType) isType()        {}
func (*ConstValueType) isType()        {}
func (*StructStateCaseType) isType()   {}
func (*StructStateSetType) isType()    {}
func (*RefStorageValueType) isType()   {}
func (*RegionParamType) isType()       {}
func (*RegionValueType) isType()       {}
func (*ErrorSetType) isType()          {}
func (*ErrorUnionType) isType()        {}
func (*OptionalType) isType()          {}
func (*TupleType) isType()             {}
func (*BitGroupType) isType()          {}
func (*ConstEnumType) isType()         {}
func (*RefType) isType()               {}
func (*ArrayType) isType()             {}
func (*DArrayType) isType()            {}
func (*ViewType) isType()              {}
func (*StoreRowsViewType) isType()     {}
func (*StoreRowViewType) isType()      {}
func (*CStrType) isType()              {}
func (*DictType) isType()              {}
func (*SetType) isType()               {}
func (*DictEntryType) isType()         {}
func (*SViewType) isType()             {}
func (*PackedEnumStoreType) isType()   {}
func (*PackedVariantViewType) isType() {}
func (*EnumType) isType()              {}
func (*StructType) isType()            {}
func (*OpaqueType) isType()            {}
func (*GenericInstanceType) isType()   {}
func (*AggregateStateType) isType()    {}
func (*FuncType) isType()              {}

func (*ShapeParam) isShape()    {}
func (*NamedShape) isShape()    {}
func (*WildcardShape) isShape() {}
func (*FreshShape) isShape()    {}

func (*InvalidType) String() string { return "<invalid>" }
func (*NeverType) String() string   { return "<never>" }
func (*NullType) String() string    { return "null" }
func (t *BuiltinType) String() string {
	return t.Name
}
func (t *BitIntType) String() string {
	if t == nil {
		return "<invalid-bitint>"
	}
	return BitIntName(t.Signed, t.Width)
}
func (t *IDType) String() string {
	if t == nil || t.Tag == nil {
		return "id[<invalid>]"
	}
	return fmt.Sprintf("id[%s]", t.Tag.String())
}
func (t *AddressSpaceType) String() string {
	if t == nil || t.Elem == nil {
		return "Address[<invalid>]"
	}
	switch t.Space {
	case "guest":
		return fmt.Sprintf("GuestVAddr[%s]", t.Elem.String())
	case "host":
		return fmt.Sprintf("HostPtr[%s]", t.Elem.String())
	case "native_mapped_guest":
		return fmt.Sprintf("NativeMappedGuestPtr[%s]", t.Elem.String())
	default:
		return fmt.Sprintf("Address[%s,%s]", t.Space, t.Elem.String())
	}
}
func (t *RegionParamType) String() string {
	if t == nil {
		return "<invalid-region-param>"
	}
	return t.Name
}
func (t *RegionValueType) String() string {
	if t == nil {
		return "<invalid-region>"
	}
	return t.Name
}
func (t *TypeParamType) String() string { return t.Name }
func (t *ConstParamType) String() string {
	return t.Name
}
func (t *ConstValueType) String() string {
	if t == nil {
		return "<invalid-const>"
	}
	switch t.Value.Kind {
	case ConstInt:
		return fmt.Sprintf("%d", t.Value.Int)
	case ConstBool:
		if t.Value.Bool {
			return "true"
		}
		return "false"
	case ConstString:
		return fmt.Sprintf("%q", t.Value.String)
	case ConstTuple:
		return "<const tuple>"
	case ConstList:
		return "<const list>"
	case ConstRecord:
		return "<const record>"
	case ConstDict:
		return "<const dict>"
	case ConstOptional:
		if !t.Value.Some {
			return "<const none>"
		}
		if t.Value.Value == nil {
			return "<const optional>"
		}
		return "<const some>"
	default:
		return "<const>"
	}
}
func (t *StructStateCaseType) String() string {
	if t == nil {
		return "<invalid-struct-state>"
	}
	return t.Case
}
func (t *StructStateSetType) String() string {
	if t == nil {
		return "<invalid-struct-state-set>"
	}
	return strings.Join(t.Cases, " | ")
}
func (t *RefStorageValueType) String() string {
	if t == nil {
		return "<invalid-refstorage>"
	}
	return RefStorageName(t.Storage)
}
func (t *ErrorSetType) String() string { return t.Name }
func (t *ErrorUnionType) String() string {
	if t == nil || t.Value == nil || t.Errors == nil {
		return "<invalid-error-union>"
	}
	return fmt.Sprintf("%s | %s", t.Value.String(), t.Errors.String())
}
func (t *OptionalType) String() string {
	if t == nil || t.Value == nil {
		return "<invalid-optional>"
	}
	return t.Value.String() + "?"
}
func (t *TupleType) String() string {
	if t == nil {
		return "<invalid-tuple>"
	}
	parts := make([]string, 0, len(t.Fields))
	for _, field := range t.Fields {
		if field.Type == nil {
			if field.Name != "" {
				parts = append(parts, field.Name+": <invalid>")
			} else {
				parts = append(parts, "<invalid>")
			}
			continue
		}
		if field.Name != "" {
			parts = append(parts, field.Name+": "+field.Type.String())
			continue
		}
		parts = append(parts, field.Type.String())
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
func (t *BitGroupType) String() string {
	if t == nil {
		return "<invalid-bitgroup>"
	}
	if t.Name != "" {
		return t.Name
	}
	if t.Kind == BitGroupBitset {
		return "bitset"
	}
	return "bitfield"
}
func (t *ConstEnumType) String() string {
	if t == nil {
		return "<invalid-const-enum>"
	}
	return t.Name
}
