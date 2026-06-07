package semantic

import "elisacore/src/ast"

func (a *Analyzer) analyzeQueryExpr(expr *ast.QueryExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	expectedDArray, useExpectedDArray := contextualDArrayLiteralType(expected)
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
	pattern := ast.MoveBindPattern(&ast.MoveBindNamePattern{Position: expr.Pos(), Name: expr.Name})
	if expr.Pattern != nil {
		pattern = expr.Pattern
	}
	a.bindIterLoopPattern(loopScope, pattern, ast.IterBindValue, info.ItemType, info.ItemFacts, info.HasItemFacts)
	if expr.PatternFilter != nil {
		var valueExpr ast.Expr
		patternType := info.ItemType
		if expr.PatternFilterSubject != "" {
			if sym, ok := loopScope.Lookup(expr.PatternFilterSubject); ok {
				patternType = sym.Type
				valueExpr = &ast.Ident{Position: expr.PatternFilter.Pos(), Name: expr.PatternFilterSubject}
			} else {
				a.errorf(expr.PatternFilter.Pos(), "query where pattern filter subject %q is not bound by the query pattern", expr.PatternFilterSubject)
			}
		} else if expr.Name != "_" {
			valueExpr = &ast.Ident{Position: expr.Pos(), Name: expr.Name}
		}
		a.analyzeNestedMatchPattern(expr.PatternFilter, patternType, valueExpr, loopScope)
	}
	expr.Filter = a.rewriteFrozenTreeRowFieldFilterShorthand(loopScope, pattern, sourceType, expr.Filter)
	if expr.Filter != nil {
		// When the query source const-folds (compile-time reflection like fields(T), or a
		// const list), the predicate is evaluated at compile time over interned literals, so
		// suppress the raw-u8&-compare footgun lint inside it.
		_, sourceConst := a.evalConstExpr(expr.Source)
		prevSuppress := a.inCompileTimeQueryPredicate
		a.inCompileTimeQueryPredicate = sourceConst
		condType := a.analyzeCondExprInScope(expr.Filter, loopScope)
		a.inCompileTimeQueryPredicate = prevSuppress
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
		if expr.Owner == nil && a.staticContextDepth == 0 && a.currentTreeAllocOwner.Kind != treeAllocOwnerArena && a.activeContainerRegionName() == "" && !(useExpectedDArray && a.regionAvailableForContainer(expectedDArray)) {
			a.errorf(expr.Pos(), "each query expression requires an active in <arena>: scope")
		}
		projectionType := info.ItemType
		var expectedElem Type
		if useExpectedDArray {
			expectedElem = expectedDArray.Elem
		}
		if expr.Projection != nil {
			savedScope := a.currentScope
			a.currentScope = loopScope
			projectionType = a.analyzeValueExpr(expr.Projection, expectedElem)
			a.currentScope = savedScope
		}
		if projectionType == nil || IsInvalidType(projectionType) {
			result = invalidType
		} else {
			if useExpectedDArray && !AssignableTo(expectedDArray.Elem, projectionType) {
				valuePos := expr.Pos()
				if expr.Projection != nil {
					valuePos = expr.Projection.Pos()
				}
				a.errorf(valuePos, "each query element expects %s, got %s", expectedDArray.Elem, projectionType)
				a.reportShapeMismatchNotes(valuePos, expectedDArray.Elem, projectionType)
			}
			if expr.Projection != nil {
				moveType := projectionType
				if useExpectedDArray {
					moveType = expectedDArray.Elem
				}
				a.consumeAffineValueExpr(expr.Projection, moveType, "move into each query element")
			}
			if useExpectedDArray {
				result = expectedDArray
			} else {
				result = a.stampContainerRegion(&DArrayType{Elem: projectionType, Shape: &WildcardShape{}, SurfaceName: "darray"})
			}
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

// contextualDictLiteralType peels an expected type to a *DictType, so an empty `{}` literal
// assigned to a dict-typed target can be recognized as an empty-dict initializer (the dict
// analogue of `[]` for darray). Mutability/aggregate-state wrappers are stripped.
func contextualDictLiteralType(expected Type) (*DictType, bool) {
	if dictType, ok := StripAggregateStateType(expected).(*DictType); ok && dictType != nil {
		return dictType, true
	}
	return nil, false
}
