package semantic

import (
	"strconv"
	"strings"
	"sync"

	"elisacore/src/ast"
)

type TypeID uint64

var canonicalTypeIDRegistry = struct {
	mu   sync.RWMutex
	next TypeID
	ids  map[string]TypeID
}{
	next: 1,
	ids:  map[string]TypeID{},
}

func CanonicalTypeID(t Type) TypeID {
	id, _ := TryCanonicalTypeID(t)
	return id
}

func TryCanonicalTypeID(t Type) (TypeID, bool) {
	key, ok := canonicalTypeIDKey(t)
	if !ok {
		return 0, false
	}
	canonicalTypeIDRegistry.mu.RLock()
	id, ok := canonicalTypeIDRegistry.ids[key]
	canonicalTypeIDRegistry.mu.RUnlock()
	if ok {
		return id, true
	}
	canonicalTypeIDRegistry.mu.Lock()
	defer canonicalTypeIDRegistry.mu.Unlock()
	if id, ok := canonicalTypeIDRegistry.ids[key]; ok {
		return id, true
	}
	id = canonicalTypeIDRegistry.next
	canonicalTypeIDRegistry.next++
	canonicalTypeIDRegistry.ids[key] = id
	return id, true
}

func canonicalTypeIDKey(t Type) (string, bool) {
	var b strings.Builder
	active := map[Type]int{}
	nextCycleID := 1
	if !appendTypeIDKey(&b, t, active, &nextCycleID) {
		return "", false
	}
	return b.String(), true
}

func appendTypeIDKey(b *strings.Builder, t Type, active map[Type]int, nextCycleID *int) bool {
	if t == nil {
		appendKeyTag(b, "nil")
		return true
	}
	if id, ok := active[t]; ok {
		appendKeyTag(b, "cycle")
		appendKeyInt(b, id)
		return true
	}
	active[t] = *nextCycleID
	appendKeyTag(b, "node")
	appendKeyInt(b, *nextCycleID)
	*nextCycleID++
	defer delete(active, t)
	switch tt := t.(type) {
	case *InvalidType:
		appendKeyTag(b, "invalid")
	case *NeverType:
		appendKeyTag(b, "never")
	case *NullType:
		appendKeyTag(b, "null")
	case *BuiltinType:
		appendKeyTag(b, "builtin")
		appendKeyString(b, tt.Name)
	case *IDType:
		appendKeyTag(b, "id")
		if !appendTypeIDKey(b, tt.Tag, active, nextCycleID) || !appendTypeIDKey(b, tt.Storage, active, nextCycleID) {
			return false
		}
	case *AddressSpaceType:
		appendKeyTag(b, "addrspace")
		appendKeyString(b, tt.Space)
		if !appendTypeIDKey(b, tt.Elem, active, nextCycleID) || !appendTypeIDKey(b, tt.Storage, active, nextCycleID) {
			return false
		}
	case *TypeParamType:
		appendKeyTag(b, "typeparam")
		appendKeyString(b, tt.Name)
	case *ConstParamType:
		appendKeyTag(b, "constparam")
		appendKeyString(b, tt.Name)
		if !appendTypeIDKey(b, tt.ValueType, active, nextCycleID) {
			return false
		}
	case *ConstValueType:
		appendKeyTag(b, "constvalue")
		appendKeyInt(b, int(tt.Value.Kind))
		appendKeyInt64(b, tt.Value.Int)
		appendKeyString(b, tt.Value.String)
		appendKeyBool(b, tt.Value.Bool)
	case *StructStateCaseType:
		appendKeyTag(b, "structstatecase")
		appendKeyString(b, tt.StructName)
		appendKeyString(b, tt.Case)
	case *StructStateSetType:
		appendKeyTag(b, "structstateset")
		appendKeyString(b, tt.StructName)
		appendKeyStringSlice(b, tt.Cases)
	case *RefStorageValueType:
		appendKeyTag(b, "refstoragevalue")
		appendKeyInt(b, int(tt.Storage))
	case *RegionParamType:
		appendKeyTag(b, "regionparam")
		appendKeyString(b, tt.Name)
	case *RegionValueType:
		appendKeyTag(b, "regionvalue")
		appendKeyString(b, tt.Name)
	case *ErrorSetType:
		appendKeyTag(b, "errorset")
		appendKeyStringSlice(b, tt.Tags)
		appendKeyStringSlice(b, tt.Params)
	case *ErrorUnionType:
		appendKeyTag(b, "errorunion")
		if !appendTypeIDKey(b, tt.Value, active, nextCycleID) || !appendTypeIDKey(b, tt.Errors, active, nextCycleID) {
			return false
		}
	case *OptionalType:
		appendKeyTag(b, "optional")
		if !appendTypeIDKey(b, tt.Value, active, nextCycleID) {
			return false
		}
	case *TupleType:
		appendKeyTag(b, "tuple")
		appendKeyLen(b, len(tt.Fields))
		for _, field := range tt.Fields {
			if !appendTypeIDKey(b, field.Type, active, nextCycleID) {
				return false
			}
		}
	case *ConstEnumType:
		appendKeyTag(b, "constenum")
		appendKeyString(b, tt.Name)
	case *RefType:
		appendKeyTag(b, "ref")
		appendKeyBool(b, tt.Mutable)
		appendKeyInt(b, int(tt.State))
		appendKeyInt(b, int(tt.Storage))
		appendKeyString(b, tt.Region)
		if !appendTypeIDKey(b, tt.Elem, active, nextCycleID) {
			return false
		}
	case *ArrayType:
		appendKeyTag(b, "array")
		appendKeyBool(b, tt.HasConstSize)
		appendKeyInt64(b, tt.ConstSize)
		appendKeyString(b, tt.Size)
		appendKeyString(b, tt.ConstParam)
		if !appendTypeIDKey(b, tt.Elem, active, nextCycleID) {
			return false
		}
	case *DArrayType:
		appendKeyTag(b, "darray")
		if !appendTypeIDKey(b, tt.Elem, active, nextCycleID) || !appendShapeIDKey(b, tt.Shape) {
			return false
		}
	case *ViewType:
		appendKeyTag(b, "darrayview")
		if !appendTypeIDKey(b, tt.Elem, active, nextCycleID) {
			return false
		}
	case *StoreRowsViewType:
		if tt == nil || tt.Store == nil {
			return false
		}
		appendKeyTag(b, "storerows")
		if !appendTypeIDKey(b, tt.Store, active, nextCycleID) {
			return false
		}
	case *StoreRowViewType:
		if tt == nil || tt.Store == nil {
			return false
		}
		appendKeyTag(b, "storerow")
		if !appendTypeIDKey(b, tt.Store, active, nextCycleID) {
			return false
		}
	case *DStrType:
		appendKeyTag(b, "cstr")
		if !appendShapeIDKey(b, tt.Shape) {
			return false
		}
	case *DictType:
		appendKeyTag(b, "dict")
		if !appendTypeIDKey(b, tt.Key, active, nextCycleID) || !appendTypeIDKey(b, tt.Value, active, nextCycleID) {
			return false
		}
	case *SetType:
		appendKeyTag(b, "set")
		if !appendTypeIDKey(b, tt.Elem, active, nextCycleID) {
			return false
		}
	case *DictEntryType:
		appendKeyTag(b, "dictentry")
		appendKeyBool(b, tt.Mutable)
		if !appendTypeIDKey(b, tt.Dict, active, nextCycleID) {
			return false
		}
	case *SViewType:
		appendKeyTag(b, "sview")
	case *PackedEnumStoreType:
		appendKeyTag(b, "packedstore")
		appendKeyString(b, tt.Name)
		if !appendTypeIDKey(b, tt.State, active, nextCycleID) {
			return false
		}
	case *TreeStoreType:
		appendKeyTag(b, "treestore")
		appendKeyString(b, tt.Name)
		if !appendTypeIDKey(b, tt.State, active, nextCycleID) {
			return false
		}
	case *FrozenTreeRowsViewType:
		appendKeyTag(b, "frozen-tree-rows")
		if tt == nil || tt.Category == nil {
			return false
		}
		appendKeyString(b, tt.Category.Name)
		if !appendTypeIDKey(b, tt.Store, active, nextCycleID) {
			return false
		}
	case *PackedVariantViewType:
		if tt == nil || tt.Enum == nil || tt.Variant == nil {
			return false
		}
		appendKeyTag(b, "packedview")
		if !appendTypeIDKey(b, tt.Enum, active, nextCycleID) {
			return false
		}
		appendKeyString(b, tt.Variant.Name)
	case *TreeVariantViewType:
		if tt == nil || tt.Category == nil || tt.Variant == nil {
			return false
		}
		appendKeyTag(b, "treeview")
		if !appendTypeIDKey(b, tt.Category, active, nextCycleID) {
			return false
		}
		appendKeyString(b, tt.Variant.Name)
	case *TreeNodeType:
		appendKeyTag(b, "treenode")
		appendKeyString(b, tt.Name)
	case *TreeType:
		appendKeyTag(b, "tree")
		appendKeyString(b, tt.Name)
	case *TreeCategoryType:
		appendKeyTag(b, "treecategory")
		appendKeyString(b, tt.Name)
	case *TreeBlockType:
		appendKeyTag(b, "treeblock")
		appendKeyString(b, tt.Name)
	case *TreeStructType:
		appendKeyTag(b, "treestruct")
		appendKeyString(b, tt.Name)
	case *EnumType:
		appendKeyTag(b, "enum")
		appendKeyString(b, tt.Name)
	case *StructType:
		appendKeyTag(b, "struct")
		appendKeyString(b, tt.Name)
	case *OpaqueType:
		appendKeyTag(b, "opaque")
		appendKeyString(b, tt.Name)
	case *GenericInstanceType:
		appendKeyTag(b, "genericinstance")
		appendKeyString(b, tt.Name)
		if !appendTypeIDKey(b, tt.Base, active, nextCycleID) || !appendTypeSliceIDKey(b, tt.Args, active, nextCycleID) {
			return false
		}
	case *AggregateStateType:
		appendKeyTag(b, "aggregatestate")
		appendKeyRefStateSlice(b, aggregateStateStates(tt))
		if !appendTypeIDKey(b, tt.Base, active, nextCycleID) {
			return false
		}
	case *FuncType:
		appendKeyTag(b, "func")
		appendKeyBool(b, tt.Variadic)
		appendKeyInt(b, funcTypeExplicitParamCount(tt))
		appendKeyStringSlice(b, tt.ExplicitParamNames)
		appendKeyStringSlice(b, tt.ImplicitParamNames)
		appendGenericParamSlice(b, tt.GenericParams)
		appendKeyStringSlice(b, tt.TypeParams)
		appendKeyStringSlice(b, tt.RegionParams)
		appendKeyStringSlice(b, tt.PermissionParams)
		appendKeyStringSlice(b, tt.UsedPermissionParams)
		appendKeyStringSlice(b, tt.Permissions)
		appendKeyStringSlice(b, tt.ShapeParams)
		appendKeyStringSlice(b, tt.FreshReturnShapeParams)
		if !appendTypeSliceIDKey(b, tt.Params, active, nextCycleID) || !appendTypeIDKey(b, tt.Return, active, nextCycleID) {
			return false
		}
	default:
		return false
	}
	return true
}

func appendShapeIDKey(b *strings.Builder, shape Shape) bool {
	if shape == nil {
		appendKeyTag(b, "shape:nil")
		return true
	}
	switch ss := shape.(type) {
	case *ShapeParam:
		appendKeyTag(b, "shape:param")
		appendKeyString(b, ss.Name)
	case *NamedShape:
		appendKeyTag(b, "shape:named")
		appendKeyString(b, ss.Name)
	case *WildcardShape:
		appendKeyTag(b, "shape:wildcard")
	case *FreshShape:
		appendKeyTag(b, "shape:fresh")
		appendKeyInt(b, ss.ID)
		appendKeyString(b, ss.Label)
		appendKeyString(b, ss.Origin)
	default:
		return false
	}
	return true
}

func appendTypeSliceIDKey(b *strings.Builder, types []Type, active map[Type]int, nextCycleID *int) bool {
	appendKeyLen(b, len(types))
	for _, typ := range types {
		if !appendTypeIDKey(b, typ, active, nextCycleID) {
			return false
		}
	}
	return true
}

func appendGenericParamSlice(b *strings.Builder, params []ast.GenericParam) {
	appendKeyLen(b, len(params))
	for _, param := range params {
		appendKeyInt(b, int(param.Kind))
		appendKeyString(b, param.Name)
		appendKeyString(b, param.InterfaceBound)
		appendKeyString(b, param.StateOwner)
		appendKeyStringSlice(b, param.StateCases)
	}
}

func appendKeyStringSlice(b *strings.Builder, values []string) {
	appendKeyLen(b, len(values))
	for _, value := range values {
		appendKeyString(b, value)
	}
}

func appendKeyRefStateSlice(b *strings.Builder, values []RefState) {
	appendKeyLen(b, len(values))
	for _, value := range values {
		appendKeyInt(b, int(value))
	}
}

func appendKeyTag(b *strings.Builder, tag string) {
	b.WriteString(tag)
	b.WriteByte('|')
}

func appendKeyString(b *strings.Builder, value string) {
	b.WriteString(strconv.Itoa(len(value)))
	b.WriteByte(':')
	b.WriteString(value)
	b.WriteByte('|')
}

func appendKeyBool(b *strings.Builder, value bool) {
	if value {
		b.WriteByte('1')
	} else {
		b.WriteByte('0')
	}
	b.WriteByte('|')
}

func appendKeyLen(b *strings.Builder, value int) {
	b.WriteString(strconv.Itoa(value))
	b.WriteByte('#')
}

func appendKeyInt(b *strings.Builder, value int) {
	b.WriteString(strconv.Itoa(value))
	b.WriteByte('|')
}

func appendKeyInt64(b *strings.Builder, value int64) {
	b.WriteString(strconv.FormatInt(value, 10))
	b.WriteByte('|')
}
