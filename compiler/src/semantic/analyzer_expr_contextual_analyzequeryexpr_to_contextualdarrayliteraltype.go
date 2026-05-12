package semantic

import "elisacore/src/ast"

func (a *Analyzer) analyzeQueryExpr(expr *ast.QueryExpr) Type {
	if expr == nil {
		return invalidType
	}
	sourceType := a.analyzeExpr(expr.Source)
	info, ok := a.resolveIterLoopSourceInfo(expr.Source, sourceType)
	if !ok {
		a.errorf(expr.Source.Pos(), "query expression currently requires an array, dynamic array, view, store.rows(), frozen tree row view, string-like iterable, ChunksExactView, source.enumerate(), children(node), or a projected tree attribute sequence, got %s", sourceType)
		info.ItemType = invalidType
	}
	if a.containsAffineHandleValues(info.ItemType, map[string]bool{}) {
		a.errorf(expr.Pos(), "query expression value iteration does not support affine element type %s; use an explicit loop with ref binding", info.ItemType)
	}
	if expr.Owner != nil {
		owner, ownerType, ok := a.classifyTreeAllocOwnerExpr(expr.Owner)
		if !ok || owner.Kind != treeAllocOwnerArena {
			a.errorf(expr.Owner.Pos(), "query expression owner must be an Arena or mutable Arena&, got %s", ownerType)
		}
		if expr.Kind != ast.QueryExprEach {
			a.errorf(expr.Owner.Pos(), "query expression owner is only valid for each projection queries")
		}
	}
	loopScope := NewScope(a.currentScope)
	pattern := &ast.MoveBindNamePattern{Position: expr.Pos(), Name: expr.Name}
	a.bindIterLoopPattern(loopScope, pattern, ast.IterBindValue, info.ItemType, info.ItemFacts, info.HasItemFacts)
	if expr.PatternFilter != nil {
		var valueExpr ast.Expr
		if expr.Name != "_" {
			valueExpr = &ast.Ident{Position: expr.Pos(), Name: expr.Name}
		}
		a.analyzeNestedMatchPattern(expr.PatternFilter, info.ItemType, valueExpr, loopScope)
	}
	if expr.Filter != nil {
		condType := a.analyzeCondExprInScope(expr.Filter, loopScope)
		if !IsBoolType(condType) {
			a.errorf(expr.Filter.Pos(), "query expression predicate must be bool, got %s", condType)
		}
	}
	var result Type
	switch expr.Kind {
	case ast.QueryExprAny, ast.QueryExprAll:
		result = a.namedTypes["bool"]
	case ast.QueryExprFirst:
		if expr.Projection != nil {
			savedScope := a.currentScope
			a.currentScope = loopScope
			projectionType := a.analyzeExpr(expr.Projection)
			a.currentScope = savedScope
			if opt, ok := IsOptionalType(projectionType); ok {
				result = opt
			} else {
				result = &OptionalType{Value: projectionType}
			}
		} else {
			result = &OptionalType{Value: info.ItemType}
		}
	case ast.QueryExprEach:
		if expr.Owner == nil && a.staticContextDepth == 0 && a.currentTreeAllocOwner.Kind != treeAllocOwnerArena {
			a.errorf(expr.Pos(), "each query expression requires an active in <arena>: scope")
		}
		if expr.Projection == nil {
			a.errorf(expr.Pos(), "each query expression requires a projection before for each")
			result = invalidType
			break
		}
		savedScope := a.currentScope
		a.currentScope = loopScope
		projectionType := a.analyzeExpr(expr.Projection)
		a.currentScope = savedScope
		if projectionType == nil || IsInvalidType(projectionType) {
			result = invalidType
		} else {
			a.consumeAffineValueExpr(expr.Projection, projectionType, "move into each query element")
			result = &DArrayType{Elem: projectionType, Shape: &WildcardShape{}, SurfaceName: "darray"}
		}
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
