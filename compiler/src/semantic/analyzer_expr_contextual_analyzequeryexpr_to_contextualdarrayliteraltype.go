package semantic

import "elisacore/src/ast"

func (a *Analyzer) analyzeQueryExpr(expr *ast.QueryExpr) Type {
	if expr == nil {
		return invalidType
	}
	sourceType := a.analyzeExpr(expr.Source)
	info, ok := a.resolveIterLoopSourceInfo(expr.Source, sourceType)
	if !ok {
		a.errorf(expr.Source.Pos(), "query expression currently requires an array, dynamic array, view, store.rows(), string-like iterable, ChunksExactView, enumerate(source), children(node), or a projected tree attribute sequence, got %s", sourceType)
		info.ItemType = invalidType
	}
	if a.containsAffineHandleValues(info.ItemType, map[string]bool{}) {
		a.errorf(expr.Pos(), "query expression value iteration does not support affine element type %s; use an explicit loop with ref binding", info.ItemType)
	}
	loopScope := NewScope(a.currentScope)
	pattern := &ast.MoveBindNamePattern{Position: expr.Pos(), Name: expr.Name}
	a.bindIterLoopPattern(loopScope, pattern, ast.IterBindValue, info.ItemType, info.ItemFacts, info.HasItemFacts)
	condType := a.analyzeCondExprInScope(expr.Filter, loopScope)
	if !IsBoolType(condType) {
		a.errorf(expr.Filter.Pos(), "query expression predicate must be bool, got %s", condType)
	}
	var result Type
	switch expr.Kind {
	case ast.QueryExprAny, ast.QueryExprAll:
		result = a.namedTypes["bool"]
	case ast.QueryExprFirst:
		result = &OptionalType{Value: info.ItemType}
	case ast.QueryExprCount:
		result = a.namedTypes["usize"]
	default:
		result = invalidType
	}
	a.exprTypes[expr] = result
	return result
}
func contextualArrayLiteralType(expected Type) (*ArrayType, bool) {
	arrayType, ok := expected.(*ArrayType)
	if !ok {
		return nil, false
	}
	return arrayType, true
}
func contextualDArrayLiteralType(expected Type) (*DArrayType, bool) {
	darrayType, ok := expected.(*DArrayType)
	if !ok {
		return nil, false
	}
	return darrayType, true
}
