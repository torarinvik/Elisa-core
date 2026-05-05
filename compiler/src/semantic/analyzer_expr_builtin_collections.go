package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) analyzeBuiltinDarrayPushCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "push" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray push expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "darray push does not support named arguments")
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray push requires a mutable darray receiver")
	}
	if a.currentTreeAllocOwner.Kind != treeAllocOwnerArena {
		a.errorf(expr.Pos(), "darray push requires an active in <arena>: scope")
	}
	argType := a.analyzeValueExpr(expr.Args[0], darrayType.Elem)
	if !AssignableTo(darrayType.Elem, argType) {
		a.errorf(expr.Args[0].Pos(), "darray push expects %s, got %s", darrayType.Elem, argType)
	}
	a.consumeAffineValueExpr(expr.Args[0], darrayType.Elem, "move into darray push")
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{
			Elem:            darrayType,
			Mutable:         true,
			State:           RefStateNonNull,
			Storage:         RefStorageAny,
			ExplicitStorage: true,
		}
	}
	a.exprTypes[expr.Func] = &FuncType{
		Name:   "darray.push",
		Params: []Type{resultType, darrayType.Elem},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDarrayExtendCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "extend" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray extend expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "darray extend does not support named arguments")
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray extend requires a mutable darray receiver")
	}
	if a.currentTreeAllocOwner.Kind != treeAllocOwnerArena {
		a.errorf(expr.Pos(), "darray extend requires an active in <arena>: scope")
	}
	sourceType := a.analyzeValueExpr(expr.Args[0], nil)
	if !builtinDArrayExtendSourceCompatible(darrayType.Elem, sourceType) {
		a.errorf(expr.Args[0].Pos(), "darray extend expects a compatible darray, dview, or array source of %s, got %s", darrayType.Elem, sourceType)
	}
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{
			Elem:            darrayType,
			Mutable:         true,
			State:           RefStateNonNull,
			Storage:         RefStorageAny,
			ExplicitStorage: true,
		}
	}
	a.exprTypes[expr.Func] = &FuncType{
		Name:   "darray.extend",
		Params: []Type{resultType, sourceType},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDarrayReserveCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "reserve" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray reserve expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "darray reserve does not support named arguments")
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray reserve requires a mutable darray receiver")
	}
	if a.currentTreeAllocOwner.Kind != treeAllocOwnerArena {
		a.errorf(expr.Pos(), "darray reserve requires an active in <arena>: scope")
	}
	usizeType := a.namedTypes["usize"]
	argType := a.analyzeValueExpr(expr.Args[0], usizeType)
	if !AssignableTo(usizeType, argType) {
		a.errorf(expr.Args[0].Pos(), "darray reserve expects %s, got %s", usizeType, argType)
	}
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{
			Elem:            darrayType,
			Mutable:         true,
			State:           RefStateNonNull,
			Storage:         RefStorageAny,
			ExplicitStorage: true,
		}
	}
	a.exprTypes[expr.Func] = &FuncType{
		Name:   "darray.reserve",
		Params: []Type{resultType, usizeType},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDarrayClearCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "clear" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	if len(expr.Args) != 0 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray clear expects 0 arguments, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray clear requires a mutable darray receiver")
	}
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{
			Elem:            darrayType,
			Mutable:         true,
			State:           RefStateNonNull,
			Storage:         RefStorageAny,
			ExplicitStorage: true,
		}
	}
	a.exprTypes[expr.Func] = &FuncType{Name: "darray.clear", Params: []Type{resultType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDarrayTruncateCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "truncate" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil {
		return nil, false
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray truncate expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray truncate requires a mutable darray receiver")
	}
	usizeType := a.namedTypes["usize"]
	argType := a.analyzeValueExpr(expr.Args[0], usizeType)
	if !AssignableTo(usizeType, argType) {
		a.errorf(expr.Args[0].Pos(), "darray truncate expects %s, got %s", usizeType, argType)
	}
	resultType := receiverRefType
	if resultType == nil {
		resultType = &RefType{
			Elem:            darrayType,
			Mutable:         true,
			State:           RefStateNonNull,
			Storage:         RefStorageAny,
			ExplicitStorage: true,
		}
	}
	a.exprTypes[expr.Func] = &FuncType{Name: "darray.truncate", Params: []Type{resultType, usizeType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func builtinStoreResultRefType(storeType *StructType, receiverRefType *RefType) *RefType {
	if receiverRefType != nil {
		return receiverRefType
	}
	return &RefType{Elem: storeType, Mutable: true, State: RefStateNonNull, Storage: RefStorageAny, ExplicitStorage: true}
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
	if a.currentTreeAllocOwner.Kind != treeAllocOwnerArena {
		a.errorf(expr.Pos(), "store push requires an active in <arena>: scope")
	}
	if len(expr.Args) != len(storeType.StoreFieldOrder) {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "store push expects %d arguments, got %d", len(storeType.StoreFieldOrder), len(expr.Args))
		resultType := builtinStoreResultRefType(storeType, receiverRefType)
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
	resultType := builtinStoreResultRefType(storeType, receiverRefType)
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
	if a.currentTreeAllocOwner.Kind != treeAllocOwnerArena {
		a.errorf(expr.Pos(), "store reserve requires an active in <arena>: scope")
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
	switch tt := sourceType.(type) {
	case *DArrayType:
		return SameType(elemType, tt.Elem)
	case *DArrayViewType:
		return SameType(elemType, tt.Elem)
	case *ArrayType:
		return SameType(elemType, tt.Elem)
	case *RefType:
		if tt == nil || tt.Elem == nil {
			return false
		}
		switch inner := tt.Elem.(type) {
		case *DArrayType:
			return SameType(elemType, inner.Elem)
		case *DArrayViewType:
			return SameType(elemType, inner.Elem)
		case *ArrayType:
			return SameType(elemType, inner.Elem)
		}
	}
	return false
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
	return Field{Name: fieldName, Type: darrayType.Elem, Mutable: false}, true
}
