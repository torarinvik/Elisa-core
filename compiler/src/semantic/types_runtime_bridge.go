package semantic

func dynArrayRuntimeInstance(t Type) (*GenericInstanceType, bool) {
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi.Name != "DynArray" || len(gi.Args) != 1 {
		return nil, false
	}
	return gi, true
}

type runtimeBridgeKind int

const (
	runtimeBridgeNone runtimeBridgeKind = iota
	runtimeBridgeDArrayDynArray
	runtimeBridgeDArrayViewDynArrayView
	runtimeBridgeDListCtxList
	runtimeBridgeDListViewCtxListView
	runtimeBridgeDArrayCtxList
	runtimeBridgeDStrU8Ref
	runtimeBridgeSViewCtxStringView
)

type runtimeBridgeMatch struct {
	Kind          runtimeBridgeKind
	DArray        *DArrayType
	DArrayView    *DArrayViewType
	DList         *DListType
	DListView     *DListViewType
	DynArray      *GenericInstanceType
	DynArrayView  *StructType
	CtxList       *RefType
	CtxListView   *StructType
	DStr          *DStrType
	U8Ref         *RefType
	SView         *SViewType
	CtxStringView *StructType
}

func isVoidRefType(t Type) bool {
	ref, ok := t.(*RefType)
	if !ok {
		return false
	}
	builtin, ok := ref.Elem.(*BuiltinType)
	return ok && builtin.Name == "void"
}

func ctxListRuntimeRef(t Type) (*RefType, bool) {
	ref, ok := t.(*RefType)
	if !ok {
		return nil, false
	}
	st, ok := ref.Elem.(*StructType)
	if !ok || st.Name != "CtxList" {
		return nil, false
	}
	return ref, true
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

func ctxListViewRuntimeType(t Type) (*StructType, bool) {
	st, ok := t.(*StructType)
	if !ok || st.Name != "CtxListView" {
		return nil, false
	}
	return st, true
}

func dynArrayViewRuntimeType(t Type) (*StructType, bool) {
	st, ok := t.(*StructType)
	if !ok || st.Name != "DynArrayView" {
		return nil, false
	}
	return st, true
}

func ctxStringViewRuntimeType(t Type) (*StructType, bool) {
	st, ok := t.(*StructType)
	if !ok || st.Name != "CtxStringView" {
		return nil, false
	}
	return st, true
}

func classifyRuntimeBridge(a, b Type) (runtimeBridgeMatch, bool) {
	if da, ok := a.(*DArrayType); ok {
		if dynArray, ok := dynArrayRuntimeInstance(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDArrayDynArray, DArray: da, DynArray: dynArray}, true
		}
		if isVoidRefType(da.Elem) {
			if ctxList, ok := ctxListRuntimeRef(b); ok {
				return runtimeBridgeMatch{Kind: runtimeBridgeDArrayCtxList, DArray: da, CtxList: ctxList}, true
			}
		}
	}
	if dav, ok := a.(*DArrayViewType); ok {
		if dynArrayView, ok := dynArrayViewRuntimeType(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDArrayViewDynArrayView, DArrayView: dav, DynArrayView: dynArrayView}, true
		}
	}
	if dl, ok := a.(*DListType); ok {
		if ctxList, ok := ctxListRuntimeRef(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDListCtxList, DList: dl, CtxList: ctxList}, true
		}
	}
	if dlv, ok := a.(*DListViewType); ok {
		if ctxListView, ok := ctxListViewRuntimeType(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDListViewCtxListView, DListView: dlv, CtxListView: ctxListView}, true
		}
	}
	if da, ok := b.(*DArrayType); ok {
		if dynArray, ok := dynArrayRuntimeInstance(a); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDArrayDynArray, DArray: da, DynArray: dynArray}, true
		}
		if isVoidRefType(da.Elem) {
			if ctxList, ok := ctxListRuntimeRef(a); ok {
				return runtimeBridgeMatch{Kind: runtimeBridgeDArrayCtxList, DArray: da, CtxList: ctxList}, true
			}
		}
	}
	if dav, ok := b.(*DArrayViewType); ok {
		if dynArrayView, ok := dynArrayViewRuntimeType(a); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDArrayViewDynArrayView, DArrayView: dav, DynArrayView: dynArrayView}, true
		}
	}
	if dl, ok := b.(*DListType); ok {
		if ctxList, ok := ctxListRuntimeRef(a); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDListCtxList, DList: dl, CtxList: ctxList}, true
		}
	}
	if dlv, ok := b.(*DListViewType); ok {
		if ctxListView, ok := ctxListViewRuntimeType(a); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDListViewCtxListView, DListView: dlv, CtxListView: ctxListView}, true
		}
	}
	if dstr, ok := a.(*DStrType); ok {
		if u8Ref, ok := u8RuntimeRef(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDStrU8Ref, DStr: dstr, U8Ref: u8Ref}, true
		}
	}
	if dstr, ok := b.(*DStrType); ok {
		if u8Ref, ok := u8RuntimeRef(a); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeDStrU8Ref, DStr: dstr, U8Ref: u8Ref}, true
		}
	}
	if sview, ok := a.(*SViewType); ok {
		if ctxStringView, ok := ctxStringViewRuntimeType(b); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeSViewCtxStringView, SView: sview, CtxStringView: ctxStringView}, true
		}
	}
	if sview, ok := b.(*SViewType); ok {
		if ctxStringView, ok := ctxStringViewRuntimeType(a); ok {
			return runtimeBridgeMatch{Kind: runtimeBridgeSViewCtxStringView, SView: sview, CtxStringView: ctxStringView}, true
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
	case runtimeBridgeSViewCtxStringView:
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
	case runtimeBridgeDArrayViewDynArrayView, runtimeBridgeDListCtxList, runtimeBridgeDListViewCtxListView, runtimeBridgeDArrayCtxList, runtimeBridgeDStrU8Ref, runtimeBridgeSViewCtxStringView:
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
	case runtimeBridgeDArrayViewDynArrayView, runtimeBridgeDListCtxList, runtimeBridgeDListViewCtxListView, runtimeBridgeDArrayCtxList, runtimeBridgeDStrU8Ref, runtimeBridgeSViewCtxStringView:
		return true
	default:
		return false
	}
}
