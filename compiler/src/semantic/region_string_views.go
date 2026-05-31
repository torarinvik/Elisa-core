package semantic

import (
	"elisacore/src/ast"
)

// Region-parameterized containers — safe `darray[u8] @r -> sview/cstr @r`
// conversions (design §5). These replace the unsafe `out[0].ref[static u8&]`
// idiom (a raw, unbounded C-string pointer built with trusted Unsafe casts):
//
//   - `.sview()` yields a bounded `{data,len}` view (len = count); a consumer
//     can never run off the end. No NUL involved. Read-only, no allocation.
//   - `.cstr()` yields a NUL-terminated `cstr` (C interop). A bare darray is NOT
//     NUL-terminated, so this is NOT a reinterpret: it writes a 0 sentinel at
//     items[count] (std::string::c_str() semantics, count unchanged), which
//     needs the region's arena for the capacity bump. Hence a mutable receiver
//     and a live region.
//
// Both results carry the darray's region r, so the escape checker (containerRegion
// -> checkReturn/StoreRegionContainerEscape) proves they cannot outlive r.

func isU8DArrayElem(darrayType *DArrayType) bool {
	if darrayType == nil {
		return false
	}
	b, ok := StripAggregateStateType(darrayType.Elem).(*BuiltinType)
	return ok && b != nil && b.Name == "u8"
}

func (a *Analyzer) analyzeBuiltinDarraySviewCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "as_sview" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, _, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil || !isU8DArrayElem(darrayType) {
		return nil, false
	}
	if len(expr.Args) != 0 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray sview expects 0 arguments, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	resultType := &SViewType{Region: darrayType.Region}
	a.exprTypes[expr.Func] = &FuncType{Name: "darray.sview", Params: []Type{receiverType}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDarrayCstrCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "as_cstr" || fieldExpr.Object == nil {
		return nil, false
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	darrayType, receiverRefType, ok := builtinDArrayPushReceiverType(receiverType)
	if !ok || darrayType == nil || !isU8DArrayElem(darrayType) {
		return nil, false
	}
	if len(expr.Args) != 0 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "darray cstr expects 0 arguments, got %d", len(expr.Args))
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	// `.cstr()` writes a NUL sentinel at items[count] (growing capacity by one
	// if needed), so it requires a mutable receiver and a live region — exactly
	// like a push. `.sview()` (read-only) needs neither.
	if !builtinDArrayPushReceiverWritable(a, fieldExpr.Object, receiverType, receiverRefType) {
		a.errorf(fieldExpr.Object.Pos(), "darray cstr requires a mutable darray receiver (it NUL-terminates in place)")
	}
	if a.staticContextDepth == 0 && a.currentTreeAllocOwner.Kind != treeAllocOwnerArena && !a.regionAvailableForContainer(darrayType) {
		a.errorf(expr.Pos(), "darray cstr requires an active in <arena>: scope to NUL-terminate")
	}
	a.checkDarrayGrowthRegionEscape(fieldExpr.Object, "cstr")
	resultType := &DStrType{Shape: &WildcardShape{}, SurfaceName: "cstr", Region: darrayType.Region}
	a.exprTypes[expr.Func] = &FuncType{Name: "darray.cstr", Params: []Type{receiverType}, Return: resultType}
	a.exprTypes[expr] = resultType
	a.invalidateStorageViewsForSource(fieldExpr.Object, storageViewMutationReason(fieldExpr.Object, "darray cstr"))
	return resultType, true
}
