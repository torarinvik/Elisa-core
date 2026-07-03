package semantic

import (
	"elisacore/src/lexer"
)

func (a *Analyzer) lookupField(objType Type, fieldName string, pos lexer.Pos) (Field, bool) {
	return a.lookupFieldWithDiagnostics(objType, fieldName, pos, true)
}

func (a *Analyzer) lookupFieldNoError(objType Type, fieldName string) (Field, bool) {
	return a.lookupFieldWithDiagnostics(objType, fieldName, lexer.Pos{}, false)
}

func (a *Analyzer) lookupFieldWithDiagnostics(objType Type, fieldName string, pos lexer.Pos, emitDiagnostics bool) (Field, bool) {
	// docs/107 guest-memory overlay: a field of a `GuestVAddr[L]` overlay carrier resolves to the
	// field's element type. The actual lowering happens in the IndexExpr accessor hook; this branch
	// keeps every field-resolving pass (alias/affine/mutation) from raising a spurious struct-field
	// error on the overlay carrier when it walks the bare `carrier.field`.
	if overlayType, ok := a.guestOverlayFieldType(objType, fieldName); ok {
		return Field{Name: fieldName, Type: overlayType, Mutable: false}, true
	}
	if field, ok := cstrSyntheticField(objType, fieldName); ok {
		return field, true
	}
	if field, ok := dictEntrySyntheticField(objType, fieldName); ok {
		return field, true
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			if emitDiagnostics {
				a.errorf(pos, "field access requires proven non-null reference, got %s", objType)
			}
			return Field{}, false
		}
		objType = ref.Elem
	}
	objType = StripAggregateStateType(objType)
	if fieldName == "count" {
		if _, ok := objType.(*ArrayType); ok {
			return Field{Name: fieldName, Type: builtinUsizeType(), Mutable: false}, true
		}
		if storeType, _, ok := builtinStoreReceiverType(objType); ok && storeType != nil && storeType.StoreDecl != nil && storeType.StoreDecl.Soa {
			return Field{Name: fieldName, Type: builtinUsizeType(), Mutable: false}, true
		}
	}
	if fieldName == "len" {
		if _, ok := objType.(*ArrayType); ok {
			return Field{Name: fieldName, Type: builtinUsizeType(), Mutable: false}, true
		}
	}
	if fieldName == "rows" {
		if storeType, _, ok := builtinStoreReceiverType(objType); ok && storeType != nil && storeType.StoreDecl != nil && storeType.StoreDecl.Soa {
			return Field{Name: fieldName, Type: &StoreRowsViewType{Store: storeType}, Mutable: false}, true
		}
	}
	if field, ok := storeRowViewField(objType, fieldName); ok {
		return field, true
	}
	if dictType, ok := objType.(*DictType); ok && !dictSupportsRuntimeBackedOps(dictType) {
		if emitDiagnostics {
			a.errorf(pos, "%s", runtimeBackedDictSupportDiagnostic(dictType))
		}
		return Field{}, false
	}
	if setType, ok := objType.(*SetType); ok && !setSupportsRuntimeBackedOps(setType) {
		if emitDiagnostics {
			a.errorf(pos, "%s", runtimeBackedSetSupportDiagnostic(setType))
		}
		return Field{}, false
	}
	if field, ok := packedStoreSyntheticField(objType, fieldName); ok {
		return field, true
	}
	if viewType, ok := objType.(*PackedVariantViewType); ok {
		field, ok := viewType.Field(fieldName)
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "%s has no field %q", viewType, fieldName)
			}
			return Field{}, false
		}
		return field, true
	}
	if enumType, ok := objType.(*EnumType); ok && enumType.Packed {
		field, ok := enumType.Common[fieldName]
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "packed enum %q has no common field %q", enumType.Name, fieldName)
			}
			return Field{}, false
		}
		return field, true
	}
	if runtimeBacked := a.runtimeBackedStructType(objType); runtimeBacked != nil {
		objType = runtimeBacked
	}
	switch t := objType.(type) {
	case *BitGroupType:
		member, ok := t.MemberMap[fieldName]
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "%s has no packed member %q", t, fieldName)
			}
			return Field{}, false
		}
		return Field{Name: member.Name, Type: member.Type, Mutable: true}, true
	case *TupleType:
		for _, field := range t.Fields {
			if field.Name == fieldName {
				return Field{Name: field.Name, Type: field.Type}, true
			}
		}
		if emitDiagnostics {
			a.errorf(pos, "tuple %s has no field %q", t, fieldName)
		}
		return Field{}, false
	case *StructType:
		field, ok := t.Fields[fieldName]
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "struct %q has no field %q", t.Name, fieldName)
			}
			return Field{}, false
		}
		return field, true
	case *GenericInstanceType:
		baseStruct, ok := t.Base.(*StructType)
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "field access requires struct type, got %s", objType)
			}
			return Field{}, false
		}
		field, ok := baseStruct.Fields[fieldName]
		if !ok {
			if emitDiagnostics {
				a.errorf(pos, "struct %q has no field %q", baseStruct.Name, fieldName)
			}
			return Field{}, false
		}
		bindings := genericBindingsForStructInstance(baseStruct, t.Args)
		regionBindings := regionBindingsForStructInstance(baseStruct, t.Args)
		field.Type = a.substituteType(field.Type, bindings, nil, regionBindings, nil)
		return field, true
	default:
		if emitDiagnostics {
			a.errorf(pos, "field access requires struct type, got %s", objType)
		}
		return Field{}, false
	}
}

func (a *Analyzer) runtimeBackedStructType(t Type) Type {
	if dav, ok := t.(*ViewType); ok {
		base, ok := a.namedTypes["DynArrayView"]
		if !ok {
			return nil
		}
		_ = dav
		return base
	}
	if _, ok := t.(*SViewType); ok {
		base, ok := a.namedTypes["StringView"]
		if !ok {
			return nil
		}
		return base
	}
	if dict, ok := t.(*DictType); ok {
		if !dictSupportsRuntimeBackedOps(dict) {
			return nil
		}
		base, ok := a.namedTypes["DynDict"]
		if !ok {
			return nil
		}
		return &GenericInstanceType{Name: "DynDict", Base: base, Args: []Type{dict.Key, dict.Value}}
	}
	if set, ok := t.(*SetType); ok {
		if !setSupportsRuntimeBackedOps(set) {
			return nil
		}
		base, ok := a.namedTypes["DynSet"]
		if !ok {
			return nil
		}
		return &GenericInstanceType{Name: "DynSet", Base: base, Args: []Type{set.Elem}}
	}
	darray, ok := t.(*DArrayType)
	if !ok {
		return nil
	}
	base, ok := a.namedTypes["DynArray"]
	if !ok {
		return nil
	}
	return &GenericInstanceType{Name: "DynArray", Base: base, Args: []Type{darray.Elem}}
}

func valueOnlyIndexKind(t Type) (string, bool) {
	if _, ok := t.(*PackedEnumStoreType); ok {
		return "packed store index result", true
	}
	if view, ok := t.(*ViewType); ok {
		if view.SurfaceName == "packedview" {
			return "packed store view index result", true
		}
		if view.SurfaceName == "packedtags" {
			return "packed store tag view index result", true
		}
		// A read-only `view[T]` is a look-only borrow: indexed writes through it are rejected. A
		// `mutable view[T]` (Mutable) permits write-through, so it is NOT value-only. To mutate via a
		// read-only view you must instead hold a `mutable view[T]` (which requires a mutable source).
		if !view.Mutable {
			return "read-only view index result", true
		}
	}
	if _, ok := t.(*DStrType); ok {
		return "string index", true
	}
	if isStringViewType(t) {
		return "string view index", true
	}
	if _, ok := ChunksExactViewItemType(t); ok {
		return "chunked view index result", true
	}
	ref, ok := t.(*RefType)
	if !ok {
		return "", false
	}
	if _, ok := ref.Elem.(*PackedEnumStoreType); ok {
		return "packed store index result", true
	}
	if view, ok := ref.Elem.(*ViewType); ok {
		if view.SurfaceName == "packedview" {
			return "packed store view index result", true
		}
		if view.SurfaceName == "packedtags" {
			return "packed store tag view index result", true
		}
		if !view.Mutable {
			return "read-only view index result", true
		}
	}
	if _, ok := ref.Elem.(*DStrType); ok {
		return "string index", true
	}
	if isStringViewType(ref.Elem) {
		return "string view index", true
	}
	if _, ok := ChunksExactViewItemType(ref.Elem); ok {
		return "chunked view index result", true
	}
	return "", false
}

func isStringViewType(t Type) bool {
	if _, ok := t.(*SViewType); ok {
		return true
	}
	// view[u8] is the unified string slice (Step 4): a borrowed u8 window
	// carries string behavior, so it is recognized everywhere sview is.
	if vt, ok := t.(*ViewType); ok && vt != nil {
		if b, ok := StripAggregateStateType(vt.Elem).(*BuiltinType); ok && b != nil && b.Name == "u8" {
			return true
		}
	}
	return isRuntimeStringViewType(t)
}

func isRuntimeStringViewType(t Type) bool {
	st, ok := t.(*StructType)
	return ok && st.Name == "StringView"
}

func cstrSyntheticField(t Type, fieldName string) (Field, bool) {
	if fieldName != "len" {
		return Field{}, false
	}
	if _, ok := t.(*DStrType); ok {
		return Field{Name: "len", Type: builtinI64Type(), Mutable: false}, true
	}
	ref, ok := t.(*RefType)
	if !ok {
		return Field{}, false
	}
	if _, ok := ref.Elem.(*DStrType); ok {
		return Field{Name: "len", Type: builtinI64Type(), Mutable: false}, true
	}
	// A string literal has type RefType{Elem: u8, Static}. Allow `.len` on it so
	// `"abc".len` type-checks — the const-eval layer resolves this to an integer
	// constant, and the prover then discharges length bounds at the const tier.
	if u8Ref, ok := ref.Elem.(*BuiltinType); ok && u8Ref.Name == "u8" {
		return Field{Name: "len", Type: builtinI64Type(), Mutable: false}, true
	}
	return Field{}, false
}

func dictEntrySyntheticField(t Type, fieldName string) (Field, bool) {
	entryType, _, ok := builtinDictEntryReceiverType(t)
	if !ok || entryType == nil || entryType.Dict == nil {
		return Field{}, false
	}
	switch fieldName {
	case "found":
		return Field{Name: "found", Type: &BuiltinType{Name: "bool"}, Mutable: false}, true
	case "value":
		return Field{Name: "value", Type: builtinDictEntryValueRefType(entryType.Dict, entryType.Mutable), Mutable: false}, true
	default:
		return Field{}, false
	}
}

func packedStoreSyntheticField(t Type, fieldName string) (Field, bool) {
	storeType, ok := t.(*PackedEnumStoreType)
	if !ok {
		return Field{}, false
	}
	switch fieldName {
	case "count":
		return Field{Name: "count", Type: builtinUsizeType(), Mutable: false}, true
	case "tags":
		if !IsFrozenPackedEnumStoreType(storeType) || storeType.Enum == nil || storeType.Enum.TagType == nil {
			return Field{}, false
		}
		return Field{Name: "tags", Type: &ViewType{Elem: storeType.Enum.TagType, SurfaceName: "packedtags"}, Mutable: false}, true
	}
	return Field{}, false
}

func builtinI64Type() Type {
	return &BuiltinType{Name: "i64"}
}

func builtinUsizeType() Type {
	return &BuiltinType{Name: "usize"}
}

func builtinCharType() Type {
	return &BuiltinType{Name: "char"}
}

type runtimeStringKind int

const (
	runtimeStringNone runtimeStringKind = iota
	runtimeStringDStr
	runtimeStringView
	runtimeStringRaw
)

func runtimeStringComparable(left Type, right Type) bool {
	leftKind := runtimeStringKindOf(left)
	rightKind := runtimeStringKindOf(right)
	if leftKind == runtimeStringNone || rightKind == runtimeStringNone {
		return false
	}
	if leftKind == runtimeStringRaw && rightKind == runtimeStringRaw {
		return false
	}
	return leftKind != runtimeStringNone && rightKind != runtimeStringNone
}

func runtimeStringKindOf(t Type) runtimeStringKind {
	if t == nil {
		return runtimeStringNone
	}
	if _, ok := t.(*DStrType); ok {
		return runtimeStringDStr
	}
	if isStringViewType(t) {
		return runtimeStringView
	}
	ref, ok := t.(*RefType)
	if !ok {
		return runtimeStringNone
	}
	if builtin, ok := ref.Elem.(*BuiltinType); ok && builtin.Name == "u8" {
		return runtimeStringRaw
	}
	if isStringViewType(ref.Elem) {
		return runtimeStringView
	}
	return runtimeStringNone
}
