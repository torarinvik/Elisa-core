package semantic

import (
	"elisacore/src/ast"
)

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
	a.recordCompilerBuiltinHelperCall(expr, "darray.reserve")
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
	if a.currentAllocExpr == nil && !a.regionAvailableForContainer(darrayType) && !a.growthReceiverIsProgramLifetime(expr, fieldExpr.Object) {
		a.errorf(expr.Pos(), "%s", NoGrowthRegionMessage("darray reserve"))
	}
	a.checkDarrayGrowthRegionEscape(fieldExpr.Object, "reserve")
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
	a.invalidateStorageViewsForSource(fieldExpr.Object, storageViewMutationReason(fieldExpr.Object, "darray reserve"))
	a.invalidateIndexBoundsForContainer(fieldExpr.Object)
	return resultType, true
}

// rejectAffineElementDrop reports an error when a container operation would discard
// must-consume (affine) elements without consuming them. Silently dropping such a value is
// exactly the leak the affine system exists to prevent; the only sound way to remove affine
// elements is a consuming move-drain (`for x in move c:`), which moves each element out and
// forces the body to consume it.
func (a *Analyzer) rejectAffineElementDrop(obj ast.Expr, elemType Type, op string) {
	if a == nil || obj == nil || elemType == nil {
		return
	}
	if !a.containsAffineHandleValues(elemType, map[string]bool{}) {
		return
	}
	a.errorf(obj.Pos(), "%s would drop must-consume element(s) of type %s without consuming them; drain the container with `for x in move c:` and consume each element instead", op, elemType)
}

// analyzeBuiltinDarrayResizeCall handles `da.resize(n)`: presize to exactly n live
// elements in one allocation (capacity >= n, count := n), so a fill loop writes by
// index with no per-element push/capacity-check. Mirrors reserve, then seals the length.
func (a *Analyzer) analyzeBuiltinDarrayResizeCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "resize" || fieldExpr.Object == nil {
		return nil, false
	}
	a.forgetDarrayNulTerminated(fieldExpr.Object)
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
		a.errorf(expr.Pos(), "darray resize expects 1 argument, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if expr.NamedArgCount() != 0 {
		a.errorf(expr.Pos(), "darray resize does not support named arguments")
	}
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray resize requires a mutable darray receiver")
	}
	if a.currentAllocExpr == nil && !a.regionAvailableForContainer(darrayType) && !a.growthReceiverIsProgramLifetime(expr, fieldExpr.Object) {
		a.errorf(expr.Pos(), "%s", NoGrowthRegionMessage("darray resize"))
	}
	// resize can shrink (dropping the tail) and, when growing, exposes zero-filled slots as live
	// elements — both unsound for must-consume element types. Reject affine-element resize.
	a.rejectAffineElementDrop(fieldExpr.Object, darrayType.Elem, "darray resize")
	a.checkDarrayGrowthRegionEscape(fieldExpr.Object, "resize")
	usizeType := a.namedTypes["usize"]
	argType := a.analyzeValueExpr(expr.Args[0], usizeType)
	if !AssignableTo(usizeType, argType) {
		a.errorf(expr.Args[0].Pos(), "darray resize expects %s, got %s", usizeType, argType)
	}
	a.recordResizeArgUpperBound(expr)
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
		Name:   "darray.resize",
		Params: []Type{resultType, usizeType},
		Return: resultType,
	}
	a.exprTypes[expr] = resultType
	a.invalidateStorageViewsForSource(fieldExpr.Object, storageViewMutationReason(fieldExpr.Object, "darray resize"))
	a.invalidateIndexBoundsForContainer(fieldExpr.Object)
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
	a.forgetDarrayNulTerminated(fieldExpr.Object)
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
	a.rejectAffineElementDrop(fieldExpr.Object, darrayType.Elem, "darray clear")
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
	a.invalidateStorageViewsForSource(fieldExpr.Object, storageViewMutationReason(fieldExpr.Object, "darray clear"))
	a.invalidateIndexBoundsForContainer(fieldExpr.Object)
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
	a.forgetDarrayNulTerminated(fieldExpr.Object)
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
	a.rejectAffineElementDrop(fieldExpr.Object, darrayType.Elem, "darray truncate")
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
	a.invalidateStorageViewsForSource(fieldExpr.Object, storageViewMutationReason(fieldExpr.Object, "darray truncate"))
	a.invalidateIndexBoundsForContainer(fieldExpr.Object)
	return resultType, true
}
