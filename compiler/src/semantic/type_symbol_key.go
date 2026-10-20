package semantic

import (
	"bytes"
	"elisacore/src/ast"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
)

// TypeSymbolKey returns a deterministic structural spelling for emitted generic
// identities. It never uses diagnostic String(), Go implementation type names,
// pointer addresses or process-assigned type IDs. Nominal types retain their
// current resolved declaration name; runtime-only container regions are erased.
// Unsupported/unresolved projections fail closed rather than share a symbol.
func TypeSymbolKey(t Type) (string, error) { return TypeSymbolKeys([]Type{t}) }

// TypeSymbolKeys shares fresh-shape alpha-renaming across the complete ordered
// argument vector: (fresh A, fresh A) differs from (fresh A, fresh B), while the
// analyzer's allocation counters and incidental node sharing are not identities.
func TypeSymbolKeys(types []Type) (string, error) { return typeSymbolKeys(types, false) }

// TypeSymbolKeysForNominalLayout normalizes a formal region and a bound region
// at the LLVM named-struct boundary: lifetime provenance has no stored bits. Semantic specialization
// keys remain distinct; this is not a general ABI equivalence or cache fingerprint.
func TypeSymbolKeysForNominalLayout(types []Type) (string, error) { return typeSymbolKeys(types, true) }

func typeSymbolKeys(types []Type, layoutRegions bool) (string, error) {
	var b bytes.Buffer
	b.WriteString("symbol-types-v1|")
	active := &symbolTypeActive{freshShapes: make(map[int]int), layoutRegions: layoutRegions}
	next := 1
	appendKeyLen(&b, len(types))
	for _, t := range types {
		if t == nil || !appendSymbolTypeKey(&b, t, active, &next) {
			return "", fmt.Errorf("unsupported or unresolved generic type argument")
		}
	}
	return b.String(), nil
}

type symbolTypeActive struct {
	typeIDActive
	freshShapes   map[int]int
	layoutRegions bool
}

func appendSymbolShapeKey(b *bytes.Buffer, shape Shape, active *symbolTypeActive) bool {
	if fresh, ok := shape.(*FreshShape); ok {
		if fresh == nil || fresh.ID <= 0 {
			return false
		}
		id, seen := active.freshShapes[fresh.ID]
		if !seen {
			id = len(active.freshShapes) + 1
			active.freshShapes[fresh.ID] = id
		}
		appendKeyTag(b, "shape:fresh")
		appendKeyInt(b, id)
		return true
	}
	return appendShapeIDKey(b, shape)
}

func appendSymbolTypeKey(b *bytes.Buffer, t Type, active *symbolTypeActive, nextCycleID *int) bool {
	if t != nil && (reflect.ValueOf(t).Kind() == reflect.Ptr && reflect.ValueOf(t).IsNil()) {
		return false
	}
	if t == nil {
		appendKeyTag(b, "nil")
		return true
	}
	if _, ok := active.lookup(t); ok {
		return false
	}
	if len(active.frames) >= semanticTraversalDepthLimit {
		return false
	}
	active.push(t, *nextCycleID)
	appendKeyTag(b, "node")
	appendKeyInt(b, *nextCycleID)
	*nextCycleID++
	defer active.pop()
	switch tt := t.(type) {
	case *InvalidType:
		return false
	case *BitIntType:
		appendKeyTag(b, "bitint")
		appendKeyBool(b, tt.Signed)
		appendKeyInt(b, tt.Width)
	case *NeverType:
		appendKeyTag(b, "never")
	case *NullType:
		appendKeyTag(b, "null")
	case *BuiltinType:
		appendKeyTag(b, "builtin")
		appendKeyString(b, tt.Name)
	case *IDType:
		appendKeyTag(b, "id")
		if !appendSymbolTypeKey(b, tt.Tag, active, nextCycleID) || !appendSymbolTypeKey(b, tt.Storage, active, nextCycleID) {
			return false
		}
	case *AddressSpaceType:
		appendKeyTag(b, "addrspace")
		appendKeyString(b, tt.Space)
		appendKeyString(b, tt.LayoutName)
		if !appendSymbolTypeKey(b, tt.Elem, active, nextCycleID) || !appendSymbolTypeKey(b, tt.Storage, active, nextCycleID) {
			return false
		}
	case *TypeParamType:
		appendKeyTag(b, "typeparam")
		appendKeyString(b, tt.Name)
	case *ConstParamType:
		appendKeyTag(b, "constparam")
		appendKeyString(b, tt.Name)
		if !appendSymbolTypeKey(b, tt.ValueType, active, nextCycleID) {
			return false
		}
	case *ConstValueType:
		appendKeyTag(b, "constvalue")
		if !appendSymbolConstValue(b, tt.Value, 0) {
			return false
		}
	case *StructStateCaseType:
		appendKeyTag(b, "structstatecase")
		appendKeyString(b, tt.StructName)
		appendKeyString(b, tt.Case)
		appendKeyBool(b, tt.Unknown)
	case *StructStateSetType:
		appendKeyTag(b, "structstateset")
		appendKeyString(b, tt.StructName)
		appendKeyStringSlice(b, tt.Cases)
		appendKeyBool(b, tt.Unknown)
	case *RefStorageValueType:
		appendKeyTag(b, "refstoragevalue")
		appendKeyInt(b, int(tt.Storage))
	case *RegionParamType:
		if active.layoutRegions {
			appendKeyTag(b, "layout-region")
		} else {
			appendKeyTag(b, "regionparam")
			appendKeyString(b, tt.Name)
		}
	case *RegionValueType:
		if active.layoutRegions {
			appendKeyTag(b, "layout-region")
		} else {
			appendKeyTag(b, "regionvalue")
			appendKeyString(b, tt.Name)
		}
	case *ErrorSetType:
		appendKeyTag(b, "errorset")
		appendKeyString(b, tt.Name)
		keys := make([]string, 0, len(tt.Payloads))
		for key := range tt.Payloads {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		appendKeyLen(b, len(keys))
		for _, key := range keys {
			appendKeyString(b, key)
			if !appendSymbolTypeSliceKey(b, tt.Payloads[key], active, nextCycleID) {
				return false
			}
		}
		appendKeyStringSlice(b, tt.Tags)
		appendKeyStringSlice(b, tt.Params)
	case *ErrorUnionType:
		appendKeyTag(b, "errorunion")
		if !appendSymbolTypeKey(b, tt.Value, active, nextCycleID) || !appendSymbolTypeKey(b, tt.Errors, active, nextCycleID) {
			return false
		}
	case *OptionalType:
		appendKeyTag(b, "optional")
		if !appendSymbolTypeKey(b, tt.Value, active, nextCycleID) {
			return false
		}
	case *TupleType:
		appendKeyTag(b, "tuple")
		appendKeyLen(b, len(tt.Fields))
		for _, field := range tt.Fields {
			appendKeyString(b, field.Name)
			if !appendSymbolTypeKey(b, field.Type, active, nextCycleID) {
				return false
			}
		}
	case *BitGroupType:
		appendKeyTag(b, "bitgroup")
		appendKeyString(b, tt.Name)
		appendKeyInt(b, int(tt.Kind))
		appendKeyInt(b, tt.BackingWidth)
		appendKeyLen(b, len(tt.Members))
		for _, member := range tt.Members {
			appendKeyString(b, member.Name)
			appendKeyInt(b, member.Offset)
			appendKeyInt(b, member.Width)
			appendKeyBool(b, member.Signed)
			if !appendSymbolTypeKey(b, member.Type, active, nextCycleID) {
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
		if !active.layoutRegions {
			appendKeyString(b, tt.Region)
		}
		appendKeyBool(b, tt.ExplicitStorage)
		appendKeyBool(b, tt.Linear)
		if !appendSymbolTypeKey(b, tt.Elem, active, nextCycleID) {
			return false
		}
	case *ArrayType:
		appendKeyTag(b, "array")
		appendKeyBool(b, tt.HasConstSize)
		if tt.HasConstSize {
			appendKeyInt64(b, tt.ConstSize)
		} else {
			appendKeyString(b, tt.Size)
			appendKeyString(b, tt.ConstParam)
		}
		if !appendSymbolTypeKey(b, tt.Elem, active, nextCycleID) {
			return false
		}
	case *DArrayType:
		appendKeyTag(b, "darray")
		if !appendSymbolTypeKey(b, tt.Elem, active, nextCycleID) || !appendSymbolShapeKey(b, tt.Shape, active) {
			return false
		}
	case *ViewType:
		appendKeyTag(b, "darrayview")
		appendKeyBool(b, tt.Mutable)
		appendKeyString(b, tt.Begin)
		appendKeyString(b, tt.End)
		if !appendSymbolTypeKey(b, tt.Elem, active, nextCycleID) {
			return false
		}
	case *StoreRowsViewType:
		if tt == nil || tt.Store == nil {
			return false
		}
		appendKeyTag(b, "storerows")
		if !appendSymbolTypeKey(b, tt.Store, active, nextCycleID) {
			return false
		}
	case *StoreRowViewType:
		if tt == nil || tt.Store == nil {
			return false
		}
		appendKeyTag(b, "storerow")
		if !appendSymbolTypeKey(b, tt.Store, active, nextCycleID) {
			return false
		}
	case *CStrType:
		appendKeyTag(b, "cstr")
		if !appendSymbolShapeKey(b, tt.Shape, active) {
			return false
		}
	case *DictType:
		appendKeyTag(b, "dict")
		if !appendSymbolTypeKey(b, tt.Key, active, nextCycleID) || !appendSymbolTypeKey(b, tt.Value, active, nextCycleID) {
			return false
		}
	case *SetType:
		appendKeyTag(b, "set")
		if !appendSymbolTypeKey(b, tt.Elem, active, nextCycleID) {
			return false
		}
	case *DictEntryType:
		appendKeyTag(b, "dictentry")
		appendKeyBool(b, tt.Mutable)
		if !appendSymbolTypeKey(b, tt.Dict, active, nextCycleID) {
			return false
		}
	case *SViewType:
		appendKeyTag(b, "sview")
		appendKeyString(b, tt.Begin)
		appendKeyString(b, tt.End)
	case *PackedEnumStoreType:
		appendKeyTag(b, "packedstore")
		appendKeyString(b, tt.Name)
		if !appendSymbolTypeKey(b, tt.State, active, nextCycleID) {
			return false
		}
	case *PackedVariantViewType:
		if tt == nil || tt.Enum == nil || tt.Variant == nil {
			return false
		}
		appendKeyTag(b, "packedview")
		if !appendSymbolTypeKey(b, tt.Enum, active, nextCycleID) {
			return false
		}
		appendKeyString(b, tt.Variant.Name)
	case *EnumType:
		appendKeyTag(b, "enum")
		appendKeyString(b, tt.Name)
	case *StructType:
		appendKeyTag(b, "struct")
		appendKeyString(b, tt.Namespace)
		appendKeyString(b, tt.Name)
	case *OpaqueType:
		appendKeyTag(b, "opaque")
		appendKeyString(b, tt.Name)
		appendKeyString(b, tt.CHeader)
		appendKeyString(b, tt.CType)
	case *GenericInstanceType:
		appendKeyTag(b, "genericinstance")
		appendKeyString(b, tt.Name)
		if !appendSymbolTypeKey(b, tt.Base, active, nextCycleID) || !appendSymbolTypeSliceKey(b, tt.Args, active, nextCycleID) {
			return false
		}
	case *AggregateStateType:
		appendKeyTag(b, "aggregatestate")
		appendKeyRefStateSlice(b, aggregateStateStates(tt))
		if !appendSymbolTypeKey(b, tt.Base, active, nextCycleID) {
			return false
		}
	case *FuncType:
		appendKeyTag(b, "func")
		appendKeyString(b, tt.CallConv)
		appendKeyBool(b, tt.IsNativeExtern)
		appendKeyBool(b, tt.RegionPolymorphic)
		appendKeyBool(b, tt.ReturnsOwnedRegion)
		appendKeyBool(b, tt.HasAsyncEntry)
		appendKeyInt(b, int(tt.SegmentTransition))
		appendKeyString(b, tt.IntrinsicName)
		appendKeyLen(b, len(tt.CParamPlan))
		for _, part := range tt.CParamPlan {
			appendKeyInt(b, part.Param)
			appendKeyInt(b, int(part.Part))
		}
		appendKeyLen(b, len(tt.OwnedParams))
		for _, owned := range tt.OwnedParams {
			appendKeyBool(b, owned)
		}
		appendKeyBool(b, tt.SinkParamsKnown)
		appendKeyLen(b, len(tt.SinkParams))
		for _, sink := range tt.SinkParams {
			appendKeyBool(b, sink)
		}

		appendKeyBool(b, tt.Variadic)
		appendKeyInt(b, funcTypeExplicitParamCount(tt))
		appendKeyStringSlice(b, tt.ExplicitParamNames)
		appendKeyStringSlice(b, tt.ImplicitParamNames)
		appendSymbolGenericParamSlice(b, tt.GenericParams, active.layoutRegions)
		appendKeyStringSlice(b, tt.TypeParams)
		if active.layoutRegions {
			appendKeyLen(b, len(tt.RegionParams))
			appendKeyLen(b, len(tt.InferredRegionParams))
			appendKeyBool(b, tt.ReturnRegion != "")
		} else {
			appendKeyStringSlice(b, tt.RegionParams)
			appendKeyStringSlice(b, tt.InferredRegionParams)
			appendKeyString(b, tt.ReturnRegion)
		}
		appendKeyStringSlice(b, tt.PermissionParams)
		appendKeyStringSlice(b, tt.UsedPermissionParams)
		appendKeyStringSlice(b, tt.Permissions)
		appendKeyStringSlice(b, tt.ShapeParams)
		appendKeyStringSlice(b, tt.FreshReturnShapeParams)
		if !appendSymbolTypeSliceKey(b, tt.Params, active, nextCycleID) || !appendSymbolTypeKey(b, tt.Return, active, nextCycleID) {
			return false
		}
	default:
		return false
	}
	return true
}

func appendSymbolTypeSliceKey(b *bytes.Buffer, types []Type, active *symbolTypeActive, next *int) bool {
	appendKeyLen(b, len(types))
	for _, typ := range types {
		if !appendSymbolTypeKey(b, typ, active, next) {
			return false
		}
	}
	return true
}

func appendSymbolConstValue(b *bytes.Buffer, value ConstValue, depth int) bool {
	if depth >= semanticTraversalDepthLimit {
		return false
	}
	appendKeyInt(b, int(value.Kind))
	switch value.Kind {
	case ConstInt:
		appendKeyInt64(b, value.Int)
	case ConstFloat:
		appendKeyString(b, strconv.FormatUint(math.Float64bits(value.Float), 16))
	case ConstBool:
		appendKeyBool(b, value.Bool)
	case ConstString:
		appendKeyString(b, value.String)
	case ConstTuple, ConstList:
		appendKeyLen(b, len(value.Elems))
		for _, elem := range value.Elems {
			if !appendSymbolConstValue(b, elem, depth+1) {
				return false
			}
		}
	case ConstRecord:
		keys := make([]string, 0, len(value.Fields))
		for key := range value.Fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		appendKeyLen(b, len(keys))
		for _, key := range keys {
			appendKeyString(b, key)
			if !appendSymbolConstValue(b, value.Fields[key], depth+1) {
				return false
			}
		}
	case ConstDict:
		// Retain literal entry order; do not assume dictionary iteration is unordered.
		appendKeyLen(b, len(value.Dict))
		for _, entry := range value.Dict {
			if !appendSymbolConstValue(b, entry.Key, depth+1) || !appendSymbolConstValue(b, entry.Value, depth+1) {
				return false
			}
		}
	case ConstOptional:
		appendKeyBool(b, value.Some)
		if value.Some {
			if value.Value == nil {
				return false
			}
			return appendSymbolConstValue(b, *value.Value, depth+1)
		}
	default:
		return false
	}
	return true
}

func appendSymbolGenericParamSlice(b *bytes.Buffer, params []ast.GenericParam, layout bool) {
	appendKeyLen(b, len(params))
	for _, param := range params {
		appendKeyInt(b, int(param.Kind))
		if layout && param.Kind == ast.GenericParamRegion {
			appendKeyString(b, "")
		} else {
			appendKeyString(b, param.Name)
		}
		appendKeyString(b, param.InterfaceBound)
		appendKeyString(b, param.StateOwner)
		appendKeyStringSlice(b, param.StateCases)
	}
}
