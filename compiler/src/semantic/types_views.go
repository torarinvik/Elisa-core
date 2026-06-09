package semantic

func SplitViewInstance(t Type) (*GenericInstanceType, bool) {
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi == nil || gi.Name != "SplitView" || len(gi.Args) != 1 {
		return nil, false
	}
	return gi, true
}

func ChunksExactViewInstance(t Type) (*GenericInstanceType, bool) {
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi == nil || gi.Name != "ChunksExactView" || len(gi.Args) != 1 {
		return nil, false
	}
	return gi, true
}

func ChunksExactViewItemType(t Type) (*DArrayViewType, bool) {
	gi, ok := ChunksExactViewInstance(t)
	if !ok {
		return nil, false
	}
	return &DArrayViewType{Elem: gi.Args[0], SurfaceName: "view"}, true
}

func EnumerateViewInstance(t Type) (*GenericInstanceType, bool) {
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi == nil || gi.Name != "EnumerateView" || len(gi.Args) != 2 {
		return nil, false
	}
	return gi, true
}

func EnumerateViewSourceType(t Type) (Type, bool) {
	gi, ok := EnumerateViewInstance(t)
	if !ok {
		return nil, false
	}
	return gi.Args[0], true
}

func EnumerateViewItemType(t Type) (Type, bool) {
	gi, ok := EnumerateViewInstance(t)
	if !ok {
		return nil, false
	}
	return gi.Args[1], true
}

func EnumerateTupleType(item Type) *TupleType {
	if item == nil {
		return nil
	}
	return &TupleType{Fields: []TupleField{{Name: "index", Type: &BuiltinType{Name: "usize"}}, {Name: "value", Type: item}}}
}

func FilteredViewInstance(t Type) (*GenericInstanceType, bool) {
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi == nil || gi.Name != "FilteredView" || len(gi.Args) != 2 {
		return nil, false
	}
	return gi, true
}

func FilteredViewSourceType(t Type) (Type, bool) {
	gi, ok := FilteredViewInstance(t)
	if !ok {
		return nil, false
	}
	return gi.Args[0], true
}

func FilteredViewItemType(t Type) (Type, bool) {
	gi, ok := FilteredViewInstance(t)
	if !ok {
		return nil, false
	}
	return gi.Args[1], true
}

func FilteredViewPredicateType(item Type) *FuncType {
	if item == nil {
		return nil
	}
	return &FuncType{Name: "func", Params: []Type{item}, Return: &BuiltinType{Name: "bool"}}
}

func TreeKindFilteredViewInstance(t Type) (*GenericInstanceType, bool) {
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi == nil || gi.Name != "TreeKindFilteredView" || len(gi.Args) != 2 {
		return nil, false
	}
	return gi, true
}

func TreeKindFilteredViewSourceType(t Type) (Type, bool) {
	gi, ok := TreeKindFilteredViewInstance(t)
	if !ok {
		return nil, false
	}
	return gi.Args[0], true
}

func TreeKindFilteredViewItemType(t Type) (Type, bool) {
	gi, ok := TreeKindFilteredViewInstance(t)
	if !ok {
		return nil, false
	}
	return gi.Args[1], true
}

func StoreRowsViewInstance(t Type) (*StoreRowsViewType, bool) {
	rowsType, ok := t.(*StoreRowsViewType)
	return rowsType, ok && rowsType != nil && rowsType.Store != nil
}

func StoreRowViewInstance(t Type) (*StoreRowViewType, bool) {
	rowType, ok := t.(*StoreRowViewType)
	return rowType, ok && rowType != nil && rowType.Store != nil
}

func TreeChildrenInstance(t Type) (*GenericInstanceType, bool) {
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi == nil || gi.Name != "TreeChildren" || len(gi.Args) != 2 {
		return nil, false
	}
	return gi, true
}

func TreeChildrenSourceType(t Type) (Type, bool) {
	gi, ok := TreeChildrenInstance(t)
	if !ok {
		return nil, false
	}
	return gi.Args[0], true
}

func TreeChildrenItemType(t Type) (Type, bool) {
	gi, ok := TreeChildrenInstance(t)
	if !ok {
		return nil, false
	}
	return gi.Args[1], true
}

func TreeAttributeSequenceInstance(t Type) (*GenericInstanceType, bool) {
	gi, ok := t.(*GenericInstanceType)
	if !ok || gi == nil || gi.Name != "TreeAttributeSeq" || len(gi.Args) != 2 {
		return nil, false
	}
	return gi, true
}

func TreeAttributeSequenceSourceType(t Type) (Type, bool) {
	gi, ok := TreeAttributeSequenceInstance(t)
	if !ok {
		return nil, false
	}
	return gi.Args[0], true
}

func TreeAttributeSequenceItemType(t Type) (Type, bool) {
	gi, ok := TreeAttributeSequenceInstance(t)
	if !ok {
		return nil, false
	}
	return gi.Args[1], true
}
