package semantic

func dynArrayRuntimeInstance(t Type) (*GenericInstanceType, bool) {
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi.Name != "DynArray" || len(gi.Args) != 1 {
		return nil, false
	}
	return gi, true
}

func dynDictRuntimeInstance(t Type) (*GenericInstanceType, bool) {
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi.Name != "DynDict" || len(gi.Args) != 2 {
		return nil, false
	}
	return gi, true
}

type runtimeBridgeKind int

const (
	runtimeBridgeNone runtimeBridgeKind = iota
	runtimeBridgeDArrayDynArray
	runtimeBridgeDArrayViewDynArrayView
	runtimeBridgeDStrU8Ref
	runtimeBridgeDictDynDict
	runtimeBridgeSViewStringView
)

type runtimeBridgeMatch struct {
	Kind         runtimeBridgeKind
	DArray       *DArrayType
	DArrayView   *ViewType
	DynArray     *GenericInstanceType
	DynArrayView *StructType
	DStr         *DStrType
	Dict         *DictType
	DynDict      *GenericInstanceType
	U8Ref        *RefType
	SView        *SViewType
	StringView   *StructType
}

func isVoidRefType(t Type) bool {
	ref, ok := t.(*RefType)
	if !ok {
		return false
	}
	builtin, ok := ref.Elem.(*BuiltinType)
	return ok && builtin.Name == "void"
}

func u8RuntimeRef(t Type) (*RefType, bool) {
	ref, ok := t.(*RefType)
	if !ok {
		return nil, false
	}
	builtin, ok := ref.Elem.(*BuiltinType)
	if !ok || builtin.Name != "u8" {
		return nil, false
	}
	return ref, true
}

func dynArrayViewRuntimeType(t Type) (*StructType, bool) {
	st, ok := t.(*StructType)
	if !ok || st.Name != "DynArrayView" {
		return nil, false
	}
	return st, true
}

func stringViewRuntimeType(t Type) (*StructType, bool) {
	st, ok := t.(*StructType)
	if !ok || st.Name != "StringView" {
		return nil, false
	}
	return st, true
}

func classifyRuntimeBridge(a, b Type) (runtimeBridgeMatch, bool) {
	if da, ok := a.(*DArrayType); ok {
		if dynArray, ok := dynArrayRuntimeInstance(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDArrayDynArray, DArray: da, DynArray: dynArray}, true
		}
	}
	if dav, ok := a.(*ViewType); ok {
		if dynArrayView, ok := dynArrayViewRuntimeType(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDArrayViewDynArrayView, DArrayView: dav, DynArrayView: dynArrayView}, true
		}
	}
	if da, ok := b.(*DArrayType); ok {
		if dynArray, ok := dynArrayRuntimeInstance(a); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDArrayDynArray, DArray: da, DynArray: dynArray}, true
		}
	}
	if dav, ok := b.(*ViewType); ok {
		if dynArrayView, ok := dynArrayViewRuntimeType(a); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDArrayViewDynArrayView, DArrayView: dav, DynArrayView: dynArrayView}, true
		}
	}
	if cstr, ok := a.(*DStrType); ok {
		if u8Ref, ok := u8RuntimeRef(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDStrU8Ref, DStr: cstr, U8Ref: u8Ref}, true
		}
	}
	if cstr, ok := b.(*DStrType); ok {
		if u8Ref, ok := u8RuntimeRef(a); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDStrU8Ref, DStr: cstr, U8Ref: u8Ref}, true
		}
	}
	if dict, ok := a.(*DictType); ok {
		if dynDict, ok := dynDictRuntimeInstance(b); ok && dictSupportsRuntimeBackedOps(dict) {
			return runtimeBridgeMatch{Kind: runtimeBridgeDictDynDict, Dict: dict, DynDict: dynDict}, true
		}
	}
	if dict, ok := b.(*DictType); ok {
		if dynDict, ok := dynDictRuntimeInstance(a); ok && dictSupportsRuntimeBackedOps(dict) {
			return runtimeBridgeMatch{Kind: runtimeBridgeDictDynDict, Dict: dict, DynDict: dynDict}, true
		}
	}
	if sview, ok := a.(*SViewType); ok {
		if stringView, ok := stringViewRuntimeType(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeSViewStringView, SView: sview, StringView: stringView}, true
		}
	}
	if sview, ok := b.(*SViewType); ok {
		if stringView, ok := stringViewRuntimeType(a); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeSViewStringView, SView: sview, StringView: stringView}, true
		}
	}
	return runtimeBridgeMatch{}, false
}

func sameTypeRuntimeCompatible(a, b Type) bool {
	bridge, ok := classifyRuntimeBridge(a, b)
	if !ok {
		return false
	}
	switch bridge.Kind {
	case runtimeBridgeDArrayDynArray:
		return SameType(bridge.DArray.Elem, bridge.DynArray.Args[0])
	case runtimeBridgeDictDynDict:
		return SameType(bridge.Dict.Key, bridge.DynDict.Args[0]) && SameType(bridge.Dict.Value, bridge.DynDict.Args[1])
	case runtimeBridgeSViewStringView:
		return true
	default:
		return false
	}
}

func assignableRuntimeCompatible(dst, src Type) bool {
	bridge, ok := classifyRuntimeBridge(dst, src)
	if !ok {
		return false
	}
	switch bridge.Kind {
	case runtimeBridgeDArrayDynArray:
		return SameType(bridge.DArray.Elem, bridge.DynArray.Args[0])
	case runtimeBridgeDictDynDict:
		return SameType(bridge.Dict.Key, bridge.DynDict.Args[0]) && SameType(bridge.Dict.Value, bridge.DynDict.Args[1])
	case runtimeBridgeDArrayViewDynArrayView, runtimeBridgeDStrU8Ref, runtimeBridgeSViewStringView:
		return true
	default:
		return false
	}
}

func patternRuntimeCompatible(pattern, actual Type) bool {
	bridge, ok := classifyRuntimeBridge(pattern, actual)
	if !ok {
		return false
	}
	switch bridge.Kind {
	case runtimeBridgeDArrayDynArray:
		if patternDArray, ok := pattern.(*DArrayType); ok {
			return matchTypePattern(patternDArray.Elem, bridge.DynArray.Args[0])
		}
		if patternDynArray, ok := dynArrayRuntimeInstance(pattern); ok {
			return matchTypePattern(patternDynArray.Args[0], bridge.DArray.Elem)
		}
		return false
	case runtimeBridgeDictDynDict:
		if patternDict, ok := pattern.(*DictType); ok {
			return matchTypePattern(patternDict.Key, bridge.DynDict.Args[0]) && matchTypePattern(patternDict.Value, bridge.DynDict.Args[1])
		}
		if patternDynDict, ok := dynDictRuntimeInstance(pattern); ok {
			return matchTypePattern(patternDynDict.Args[0], bridge.Dict.Key) && matchTypePattern(patternDynDict.Args[1], bridge.Dict.Value)
		}
		return false
	case runtimeBridgeDArrayViewDynArrayView, runtimeBridgeDStrU8Ref, runtimeBridgeSViewStringView:
		return true
	default:
		return false
	}
}
