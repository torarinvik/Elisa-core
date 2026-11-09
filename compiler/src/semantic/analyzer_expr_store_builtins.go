package semantic

import (
	"elisacore/src/ast"
)

func builtinStoreResultRefType(storeType *StructType, receiverRefType *RefType) *RefType {
	if receiverRefType != nil {
		return receiverRefType
	}
	return &RefType{Elem: storeType, Mutable: true, State: RefStateNonNull, Storage: RefStorageAny, ExplicitStorage: true}
}

func builtinStorePushResultType(storeType *StructType, receiverRefType *RefType, u32Type Type) Type {
	if storeType != nil && storeType.StoreDecl != nil && storeType.StoreDecl.Soa {
		return &IDType{Tag: storeType, Storage: u32Type}
	}
	return builtinStoreResultRefType(storeType, receiverRefType)
}

func (a *Analyzer) analyzeBuiltinStorePushCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "store push requires a mutable store receiver")
	}
	if a.currentAllocExpr == nil && !a.growthReceiverIsProgramLifetime(expr, fieldExpr.Object) {
		a.errorf(expr.Pos(), "%s", NoGrowthRegionMessage("store push"))
	}
	if len(expr.Args) != len(storeType.StoreFieldOrder) {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store push expects %d arguments, got %d", len(storeType.StoreFieldOrder), len(expr.Args))
		resultType := builtinStorePushResultType(storeType, receiverRefType, a.namedTypes["u32"])
		a.exprTypes[expr] = resultType
		return resultType, true
	}
	for i, name := range storeType.StoreFieldOrder {
		field := storeType.Fields[name]
		darrayField, ok := field.Type.(*DArrayType)
		if !ok || darrayField == nil {
			continue
		}
		argType := a.analyzeValueExpr(expr.Args[i], darrayField.Elem)
		if !AssignableTo(darrayField.Elem, argType) {
			a.errorf(expr.Args[i].Pos(), "store push argument %d (%s) expects %s, got %s", i+1, name, darrayField.Elem, argType)
		}
		a.consumeAffineValueExpr(expr.Args[i], darrayField.Elem, "move into store push")
	}
	resultType := builtinStorePushResultType(storeType, receiverRefType, a.namedTypes["u32"])
	a.exprTypes[expr.Func] = &FuncType{Name: "store.push", Params: []Type{resultType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinStoreReserveCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "reserve" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "store reserve requires a mutable store receiver")
	}
	if a.currentAllocExpr == nil && !a.growthReceiverIsProgramLifetime(expr, fieldExpr.Object) {
		a.errorf(expr.Pos(), "%s", NoGrowthRegionMessage("store reserve"))
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store reserve expects 1 argument, got %d", len(expr.Args))
	}
	usizeType := a.namedTypes["usize"]
	if len(expr.Args) >= 1 {
		argType := a.analyzeValueExpr(expr.Args[0], usizeType)
		if !AssignableTo(usizeType, argType) {
			a.errorf(expr.Args[0].Pos(), "store reserve expects %s, got %s", usizeType, argType)
		}
	}
	resultType := builtinStoreResultRefType(storeType, receiverRefType)
	a.exprTypes[expr.Func] = &FuncType{Name: "store.reserve", Params: []Type{resultType, usizeType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinStoreClearCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "clear" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "store clear requires a mutable store receiver")
	}
	if len(expr.Args) != 0 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store clear expects 0 arguments, got %d", len(expr.Args))
	}
	resultType := builtinStoreResultRefType(storeType, receiverRefType)
	a.exprTypes[expr.Func] = &FuncType{Name: "store.clear", Params: []Type{resultType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinStoreTruncateCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "truncate" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, receiverRefType, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "store truncate requires a mutable store receiver")
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store truncate expects 1 argument, got %d", len(expr.Args))
	}
	usizeType := a.namedTypes["usize"]
	if len(expr.Args) >= 1 {
		argType := a.analyzeValueExpr(expr.Args[0], usizeType)
		if !AssignableTo(usizeType, argType) {
			a.errorf(expr.Args[0].Pos(), "store truncate expects %s, got %s", usizeType, argType)
		}
	}
	resultType := builtinStoreResultRefType(storeType, receiverRefType)
	a.exprTypes[expr.Func] = &FuncType{Name: "store.truncate", Params: []Type{resultType, usizeType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinStoreRowsCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "rows" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, _, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil {
		return nil, false
	}
	if len(expr.Args) != 0 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store rows expects 0 arguments, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "store rows does not support named arguments")
	}
	resultType := &StoreRowsViewType{Store: storeType}
	a.exprTypes[expr.Func] = &FuncType{
		Name:   "store.rows",
		Params: []Type{receiverType},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinSOAValidCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "valid" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	storeType, _, ok := builtinStoreReceiverType(receiverType)
	if !ok || storeType == nil || storeType.StoreDecl == nil || !storeType.StoreDecl.Soa {
		return nil, false
	}
	rowType := &IDType{Tag: storeType, Storage: a.namedTypes["u32"]}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "soa valid expects 1 argument, got %d", len(expr.Args))
	} else {
		argType := a.analyzeValueExpr(expr.Args[0], rowType)
		if !AssignableTo(rowType, argType) {
			a.errorf(expr.Args[0].Pos(), "soa valid expects %s, got %s", rowType, argType)
		}
	}
	resultType := a.namedTypes["bool"]
	a.exprTypes[expr.Func] = &FuncType{Name: "soa.valid", Params: []Type{receiverType, rowType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func builtinDArrayPushReceiverType(t Type) (*DArrayType, *RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if darrayType, ok := t.(*DArrayType); ok && darrayType != nil {
		return darrayType, nil, true
	}
	refType, ok := t.(*RefType)
	if !ok || refType == nil {
		return nil, nil, false
	}
	darrayType, ok := refType.Elem.(*DArrayType)
	if !ok || darrayType == nil {
		return nil, nil, false
	}
	return darrayType, refType, true
}

func builtinDArrayPushReceiverWritable(a *Analyzer, receiver ast.Expr, receiverType Type, receiverRefType *RefType) bool {
	if receiverRefType != nil {
		return receiverRefType.Mutable
	}
	return a != nil && a.exprCanYieldWritableRef(receiver)
}

func builtinDArrayExtendSourceCompatible(elemType Type, sourceType Type) bool {
	if elemType == nil || sourceType == nil {
		return false
	}
	// Valid extend SOURCES are contiguous element sequences: an owning darray, a fixed
	// array, or a view (`view[T]`/`sview`). A view is a read-only BORROW, so it can never
	// be the extend TARGET — but reading its bytes into `dst` is a plain value copy
	// (`dst` owns the result, the view is untouched), and a contiguous view lowers to the
	// same `arena_memcpy` a darray source does. Element region-escape and affine-move
	// safety are enforced separately by the caller (checkNestedRegionBulkStoreEscape /
	// the affine-element check), so this only governs shape compatibility.
	switch tt := sourceType.(type) {
	case *DArrayType:
		return SameType(elemType, tt.Elem)
	case *ArrayType:
		return SameType(elemType, tt.Elem)
	case *ViewType:
		return SameType(elemType, tt.Elem)
	case *SViewType:
		// sview is a contiguous u8 byte view; its element is always u8.
		return isExtendU8Elem(elemType)
	case *RefType:
		if tt == nil || tt.Elem == nil {
			return false
		}
		switch inner := tt.Elem.(type) {
		case *DArrayType:
			return SameType(elemType, inner.Elem)
		case *ArrayType:
			return SameType(elemType, inner.Elem)
		case *ViewType:
			return SameType(elemType, inner.Elem)
		case *SViewType:
			return isExtendU8Elem(elemType)
		}
	}
	return false
}

func isExtendU8Elem(elemType Type) bool {
	b, ok := StripAggregateStateType(elemType).(*BuiltinType)
	return ok && b != nil && b.Name == "u8"
}

// isStringLitExtendArg reports whether an extend argument is a bare string literal
// (peeling parentheses) — the only static-u8& shape whose length is a compile-time
// constant, hence the only one safe to adapt to a bounded view source.
func isStringLitExtendArg(e ast.Expr) bool {
	for {
		switch n := e.(type) {
		case *ast.StringLit:
			return true
		case *ast.ParenExpr:
			if n == nil {
				return false
			}
			e = n.Inner
		default:
			return false
		}
	}
}

func darrayElemPrefersListLiteralAsSingleValue(elemType Type) bool {
	switch StripAggregateStateType(elemType).(type) {
	case *ArrayType, *DArrayType:
		return true
	default:
		return false
	}
}

func builtinStoreReceiverType(t Type) (*StructType, *RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if st, ok := StripAggregateStateType(t).(*StructType); ok && st != nil && st.Store {
		return st, nil, true
	}
	refType, ok := t.(*RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	st, ok := StripAggregateStateType(refType.Elem).(*StructType)
	if !ok || st == nil || !st.Store {
		return nil, nil, false
	}
	return st, refType, true
}

func storeRowViewField(t Type, fieldName string) (Field, bool) {
	rowType, ok := t.(*StoreRowViewType)
	if !ok || rowType == nil || rowType.Store == nil {
		return Field{}, false
	}
	field, ok := rowType.Store.Fields[fieldName]
	if !ok {
		return Field{}, false
	}
	darrayType, ok := field.Type.(*DArrayType)
	if !ok || darrayType == nil {
		return Field{}, false
	}
	return Field{Name: fieldName, Type: darrayType.Elem, Mutable: true}, true
}
