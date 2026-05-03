package semantic

import "llcontext/src/ast"

func (a *Analyzer) analyzeZipMapHelperCall(expr *ast.CallExpr) Type {
	if len(expr.Args) != 4 {
		a.errorf(expr.Pos(), "zip_map expects 4 arguments, got %d", len(expr.Args))
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	dstViewType, dstElemType, dstOK := zipMapDenseViewType(a.analyzeExpr(expr.Args[0]))
	src1ViewType, src1ElemType, src1OK := zipMapDenseViewType(a.analyzeExpr(expr.Args[1]))
	src2ViewType, src2ElemType, src2OK := zipMapDenseViewType(a.analyzeExpr(expr.Args[2]))
	callbackType := a.analyzeExpr(expr.Args[3])

	if !dstOK {
		actual := a.exprTypes[expr.Args[0]]
		if actual == nil {
			actual = invalidType
		}
		a.errorf(expr.Args[0].Pos(), "zip_map destination expects a dense view[T], got %s", actual)
	}
	if !src1OK {
		actual := a.exprTypes[expr.Args[1]]
		if actual == nil {
			actual = invalidType
		}
		a.errorf(expr.Args[1].Pos(), "zip_map source 1 expects a dense view[T], got %s", actual)
	}
	if !src2OK {
		actual := a.exprTypes[expr.Args[2]]
		if actual == nil {
			actual = invalidType
		}
		a.errorf(expr.Args[2].Pos(), "zip_map source 2 expects a dense view[T], got %s", actual)
	}
	if !dstOK || !src1OK || !src2OK {
		return a.namedTypes["void"]
	}

	if !a.exprSupportsDenseWrite(expr.Args[0]) {
		a.errorf(expr.Args[0].Pos(), "zip_map requires a writable dense destination view, got %s", dstViewType)
	}
	if !a.exprSupportsReadonlyDenseView(expr.Args[1]) {
		a.errorf(expr.Args[1].Pos(), "zip_map requires source 1 to be a readonly contiguous exact-extent view, got %s", src1ViewType)
	}
	if !a.exprSupportsReadonlyDenseView(expr.Args[2]) {
		a.errorf(expr.Args[2].Pos(), "zip_map requires source 2 to be a readonly contiguous exact-extent view, got %s", src2ViewType)
	}
	if !a.exprsHaveEqualExtentSize(expr.Args[0], expr.Args[1]) {
		a.errorf(expr.Pos(), "zip_map requires destination and source 1 to have equal extents")
	}
	if !a.exprsHaveEqualExtentSize(expr.Args[0], expr.Args[2]) {
		a.errorf(expr.Pos(), "zip_map requires destination and source 2 to have equal extents")
	}
	if !a.exprsAreDisjoint(expr.Args[0], expr.Args[1]) {
		a.errorf(expr.Pos(), "zip_map requires destination and source 1 to be provably disjoint")
	}
	if !a.exprsAreDisjoint(expr.Args[0], expr.Args[2]) {
		a.errorf(expr.Pos(), "zip_map requires destination and source 2 to be provably disjoint")
	}

	callbackFn, ok := callbackType.(*FuncType)
	if !ok {
		a.errorf(expr.Args[3].Pos(), "zip_map callback expects a function value, got %s", callbackType)
		return a.namedTypes["void"]
	}
	if callbackFn.Variadic || len(callbackFn.Params) != 2 {
		a.errorf(expr.Args[3].Pos(), "zip_map callback must have type func(A, B) -> R")
		return a.namedTypes["void"]
	}
	if len(callbackFn.Permissions) != 0 {
		a.errorf(expr.Args[3].Pos(), "zip_map callback must not declare effect permissions")
	}
	if callbackFn.Return == nil || isVoidType(callbackFn.Return) {
		a.errorf(expr.Args[3].Pos(), "zip_map callback must return a value assignable to %s", dstElemType)
		return a.namedTypes["void"]
	}
	if _, ok := callbackFn.Return.(*ErrorUnionType); ok {
		a.errorf(expr.Args[3].Pos(), "zip_map callback must not return an error union")
	}
	if !AssignableTo(callbackFn.Params[0], src1ElemType) {
		a.errorf(expr.Args[3].Pos(), "zip_map callback first parameter expects %s, got %s", callbackFn.Params[0], src1ElemType)
	}
	if !AssignableTo(callbackFn.Params[1], src2ElemType) {
		a.errorf(expr.Args[3].Pos(), "zip_map callback second parameter expects %s, got %s", callbackFn.Params[1], src2ElemType)
	}
	if !AssignableTo(dstElemType, callbackFn.Return) {
		a.errorf(expr.Args[3].Pos(), "zip_map callback result expects %s, got %s", dstElemType, callbackFn.Return)
	}
	return a.namedTypes["void"]
}
func (a *Analyzer) exprSupportsDenseWrite(expr ast.Expr) bool {
	if a == nil || expr == nil {
		return false
	}
	facts, ok := a.exprFacts[expr]
	if !ok {
		return false
	}
	return facts.Contiguous && facts.UnitStride && !facts.ReadOnly
}
func (a *Analyzer) exprSupportsReadonlyDenseView(expr ast.Expr) bool {
	if a == nil || expr == nil {
		return false
	}
	facts, ok := a.exprFacts[expr]
	if !ok {
		return false
	}
	return facts.ReadOnly && facts.Contiguous && facts.UnitStride && facts.HasExactExtent()
}
func (a *Analyzer) exprsHaveEqualExtentSize(left ast.Expr, right ast.Expr) bool {
	if a == nil {
		return false
	}
	leftFacts, ok := a.exprFacts[left]
	if !ok {
		return false
	}
	rightFacts, ok := a.exprFacts[right]
	if !ok {
		return false
	}
	return leftFacts.SameExtentSize(rightFacts)
}
func (a *Analyzer) exprsAreDisjoint(left ast.Expr, right ast.Expr) bool {
	if a == nil {
		return false
	}
	leftFacts, ok := a.exprFacts[left]
	if !ok {
		return false
	}
	rightFacts, ok := a.exprFacts[right]
	if !ok {
		return false
	}
	return leftFacts.Disjoint(rightFacts)
}
