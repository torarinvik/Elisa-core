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
	runtimeBridgeCStrU8Ref
	runtimeBridgeDictDynDict
	runtimeBridgeSViewStringView
)

type runtimeBridgeMatch struct {
	Kind         runtimeBridgeKind
	DArray       *DArrayType
	DArrayView   *ViewType
	DynArray     *GenericInstanceType
	DynArrayView *StructType
	CStr         *CStrType
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

// Dispatch once on the first operand; only compatible carrier kinds need a second check.
func classifyRuntimeBridge(a, b Type) (runtimeBridgeMatch, bool) {
	switch x := a.(type) {
	case *DArrayType:
		if y, ok := dynArrayRuntimeInstance(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDArrayDynArray, DArray: x, DynArray: y}, true
		}
	case *ViewType:
		if y, ok := dynArrayViewRuntimeType(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDArrayViewDynArrayView, DArrayView: x, DynArrayView: y}, true
		}
	case *CStrType:
		if y, ok := u8RuntimeRef(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeCStrU8Ref, CStr: x, U8Ref: y}, true
		}
	case *DictType:
		if y, ok := dynDictRuntimeInstance(b); ok && dictSupportsRuntimeBackedOps(x) {
			return runtimeBridgeMatch{Kind: runtimeBridgeDictDynDict, Dict: x, DynDict: y}, true
		}
	case *SViewType:
		if y, ok := stringViewRuntimeType(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeSViewStringView, SView: x, StringView: y}, true
		}
	case *GenericInstanceType:
		if y, ok := b.(*DArrayType); ok {
			if dyn, ok := dynArrayRuntimeInstance(x); ok {
				return runtimeBridgeMatch{Kind: runtimeBridgeDArrayDynArray, DArray: y, DynArray: dyn}, true
			}
		}
		if y, ok := b.(*DictType); ok {
			if dyn, ok := dynDictRuntimeInstance(x); ok && dictSupportsRuntimeBackedOps(y) {
				return runtimeBridgeMatch{Kind: runtimeBridgeDictDynDict, Dict: y, DynDict: dyn}, true
			}
		}
	case *StructType:
		if y, ok := b.(*ViewType); ok {
			if dyn, ok := dynArrayViewRuntimeType(x); ok {
				return runtimeBridgeMatch{Kind: runtimeBridgeDArrayViewDynArrayView, DArrayView: y, DynArrayView: dyn}, true
			}
		}
		if y, ok := b.(*SViewType); ok {
			if str, ok := stringViewRuntimeType(x); ok {
				return runtimeBridgeMatch{Kind: runtimeBridgeSViewStringView, SView: y, StringView: str}, true
			}
		}
	case *RefType:
		if y, ok := b.(*CStrType); ok {
			if ref, ok := u8RuntimeRef(x); ok {
				return runtimeBridgeMatch{Kind: runtimeBridgeCStrU8Ref, CStr: y, U8Ref: ref}, true
			}
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
	case runtimeBridgeDArrayViewDynArrayView, runtimeBridgeSViewStringView:
		return true
	case runtimeBridgeCStrU8Ref:
		// A cstr carries the invariant that its pointer is non-null and points
		// through a NUL terminator. Weakening that guarantee to a byte reference
		// is safe; a byte reference does not prove termination and must never be
		// promoted to cstr implicitly.
		_, dstIsCStr := dst.(*CStrType)
		_, srcIsCStr := src.(*CStrType)
		return !dstIsCStr || srcIsCStr
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
	case runtimeBridgeDArrayViewDynArrayView, runtimeBridgeSViewStringView:
		return true
	case runtimeBridgeCStrU8Ref:
		// Keep generic candidate matching directional too. A cstr formal may
		// accept cstr actuals only, not an arbitrary u8 pointer.
		return assignableRuntimeCompatible(pattern, actual)
	default:
		return false
	}
}
