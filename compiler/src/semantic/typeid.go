package semantic

import (
	"strconv"
	"strings"
	"sync"

	"llcontext/src/ast"
	"llcontext/src/lexer"
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
	if !appendTypeIDKey(&b, t) {
		return "", false
	}
	return b.String(), true
}

func appendTypeIDKey(b *strings.Builder, t Type) bool {
	if t == nil {
		appendKeyTag(b, "nil")
		return true
	}
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
	case *TypeParamType:
		appendKeyTag(b, "typeparam")
		appendKeyString(b, tt.Name)
	case *RefStorageParamType:
		appendKeyTag(b, "refstorageparam")
		appendKeyString(b, tt.Name)
	case *RefStorageValueType:
		appendKeyTag(b, "refstoragevalue")
		appendKeyInt(b, int(tt.Storage))
	case *RefStateParamType:
		appendKeyTag(b, "refstateparam")
		appendKeyString(b, tt.Name)
	case *RefStateValueType:
		appendKeyTag(b, "refstatevalue")
		appendKeyInt(b, int(tt.State))
	case *ErrorSetType:
		appendKeyTag(b, "errorset")
		appendKeyStringSlice(b, tt.Tags)
	case *ErrorUnionType:
		appendKeyTag(b, "errorunion")
		if !appendTypeIDKey(b, tt.Value) || !appendTypeIDKey(b, tt.Errors) {
			return false
		}
	case *OptionalType:
		appendKeyTag(b, "optional")
		if !appendTypeIDKey(b, tt.Value) {
			return false
		}
	case *ConstEnumType:
		appendKeyTag(b, "constenum")
		appendKeyString(b, tt.Name)
	case *RefType:
		appendKeyTag(b, "ref")
		appendKeyInt(b, int(tt.State))
		appendKeyString(b, tt.StateParam)
		appendKeyInt(b, int(tt.Storage))
		appendKeyString(b, tt.StorageParam)
		appendKeyString(b, tt.Region)
		if !appendTypeIDKey(b, tt.Elem) {
			return false
		}
	case *ArrayType:
		appendKeyTag(b, "array")
		appendKeyBool(b, tt.HasConstSize)
		appendKeyInt64(b, tt.ConstSize)
		appendKeyString(b, tt.Size)
		if !appendTypeIDKey(b, tt.Elem) {
			return false
		}
	case *DArrayType:
		appendKeyTag(b, "darray")
		if !appendTypeIDKey(b, tt.Elem) || !appendShapeIDKey(b, tt.Shape) {
			return false
		}
	case *ViewType:
		appendKeyTag(b, "view")
		appendKeyString(b, tt.Begin)
		appendKeyString(b, tt.End)
		if !appendTypeIDKey(b, tt.Elem) {
			return false
		}
	case *DArrayViewType:
		appendKeyTag(b, "darrayview")
		if !appendTypeIDKey(b, tt.Elem) {
			return false
		}
	case *DStrType:
		appendKeyTag(b, "dstr")
		if !appendShapeIDKey(b, tt.Shape) {
			return false
		}
	case *DictType:
		appendKeyTag(b, "dict")
		if !appendTypeIDKey(b, tt.Key) || !appendTypeIDKey(b, tt.Value) {
			return false
		}
	case *SViewType:
		appendKeyTag(b, "sview")
	case *PackedEnumStoreType:
		appendKeyTag(b, "packedstore")
		appendKeyString(b, tt.Name)
		if !appendTypeIDKey(b, tt.State) {
			return false
		}
	case *PackedVariantViewType:
		if tt == nil || tt.Enum == nil || tt.Variant == nil {
			return false
		}
		appendKeyTag(b, "packedview")
		if !appendTypeIDKey(b, tt.Enum) {
			return false
		}
		appendKeyString(b, tt.Variant.Name)
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
		if !appendTypeIDKey(b, tt.Base) || !appendTypeSliceIDKey(b, tt.Args) {
			return false
		}
	case *AggregateStateType:
		appendKeyTag(b, "aggregatestate")
		appendKeyRefStateSlice(b, aggregateStateStates(tt))
		if !appendTypeIDKey(b, tt.Base) {
			return false
		}
	case *FuncType:
		appendKeyTag(b, "func")
		appendKeyBool(b, tt.Variadic)
		appendGenericParamSlice(b, tt.GenericParams)
		appendKeyStringSlice(b, tt.TypeParams)
		appendKeyStringSlice(b, tt.RefStorageParams)
		appendKeyStringSlice(b, tt.RefStateParams)
		appendKeyStringSlice(b, tt.RegionParams)
		appendKeyStringSlice(b, tt.PermissionParams)
		appendKeyStringSlice(b, tt.UsedPermissionParams)
		appendKeyStringSlice(b, tt.Permissions)
		appendKeyStringSlice(b, tt.ShapeParams)
		appendKeyStringSlice(b, tt.FreshReturnShapeParams)
		if !appendTypeSliceIDKey(b, tt.Params) || !appendTypeIDKey(b, tt.Return) {
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

func appendTypeSliceIDKey(b *strings.Builder, types []Type) bool {
	appendKeyLen(b, len(types))
	for _, typ := range types {
		if !appendTypeIDKey(b, typ) {
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
		appendKeyPos(b, param.Position)
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

func appendKeyPos(b *strings.Builder, pos lexer.Pos) {
	appendKeyString(b, pos.File)
	appendKeyInt(b, pos.Line)
	appendKeyInt(b, pos.Col)
	appendKeyInt(b, pos.Offset)
	appendKeyInt(b, pos.EndLine)
	appendKeyInt(b, pos.EndCol)
	appendKeyInt(b, pos.EndOffset)
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
