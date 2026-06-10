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
		ownerType := a.analyzeExpr(expr.Owner)
		arenaType := a.namedTypes["Arena"]
		isArena := arenaType != nil && AssignableTo(arenaType, ownerType)
		if !isArena {
			if refT, ok := ownerType.(*RefType); ok && refT != nil && arenaType != nil {
				isArena = AssignableTo(arenaType, refT.Elem)
			}
		}
		if !isArena {
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
		if expr.Owner == nil && a.staticContextDepth == 0 && a.activeContainerRegionName() == "" && !(useExpectedDArray && a.regionAvailableForContainer(expectedDArray)) {
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
	case ast.QueryExprSum, ast.QueryExprProduct:
		valueType := info.ItemType
		if expr.Projection != nil {
			savedScope := a.currentScope
			a.currentScope = loopScope
			valueType = a.analyzeExpr(expr.Projection)
			a.currentScope = savedScope
		}
		kindName := "sum"
		if expr.Kind == ast.QueryExprProduct {
			kindName = "product"
		}
		if valueType == nil || IsInvalidType(valueType) {
			result = invalidType
		} else if !IsNumericType(valueType) {
			a.errorf(expr.Pos(), "%s query requires a numeric element, got %s", kindName, valueType)
			result = invalidType
		} else {
			result = valueType
		}
	case ast.QueryExprMin, ast.QueryExprMax:
		valueType := info.ItemType
		if expr.Projection != nil {
			savedScope := a.currentScope
			a.currentScope = loopScope
			valueType = a.analyzeExpr(expr.Projection)
			a.currentScope = savedScope
		}
		kindName := "min"
		if expr.Kind == ast.QueryExprMax {
			kindName = "max"
		}
		if valueType == nil || IsInvalidType(valueType) {
			result = invalidType
		} else if !IsNumericType(valueType) {
			a.errorf(expr.Pos(), "%s query requires a numeric element, got %s", kindName, valueType)
			result = invalidType
		} else {
			// No identity element -> optional result (null over an empty/fully-filtered sequence).
			result = &OptionalType{Value: valueType}
		}
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

func contextualSetLiteralType(expected Type) (*SetType, bool) {
	if setType, ok := StripAggregateStateType(expected).(*SetType); ok && setType != nil {
		return setType, true
	}
	return nil, false
}

// analyzeDictLiteralExpr type-checks a `{k1: v1, k2: v2, ...}` dict literal. The dict type is
// taken from the expected type when present, else inferred from the first pair; every key is
// checked assignable to the key type and every value to the value type. The key type must be
// runtime-backed (hashable), and an allocation region must be in scope (auto-region inference
// supplies one for a bare literal). Returns the dict type.
func (a *Analyzer) analyzeDictLiteralExpr(expr *ast.ListLitExpr, expected Type) Type {
	dictType, _ := contextualDictLiteralType(expected)
	var keyType, valueType Type
	if dictType != nil {
		keyType = dictType.Key
		valueType = dictType.Value
	}
	for i := range expr.Keys {
		kt := a.analyzeValueExpr(expr.Keys[i], keyType)
		var vt Type
		if i < len(expr.Elems) {
			vt = a.analyzeValueExpr(expr.Elems[i], valueType)
		} else {
			vt = invalidType
		}
		if keyType == nil {
			keyType = kt
		} else if !IsInvalidType(kt) && !AssignableTo(keyType, kt) {
			a.errorf(expr.Keys[i].Pos(), "dict literal key %d has type %s, expected %s", i, diagnosticTypeString(kt), diagnosticTypeString(keyType))
		}
		if valueType == nil {
			valueType = vt
		} else if !IsInvalidType(vt) && !AssignableTo(valueType, vt) {
			a.errorf(expr.Elems[i].Pos(), "dict literal value %d has type %s, expected %s", i, diagnosticTypeString(vt), diagnosticTypeString(valueType))
		}
	}
	if keyType == nil || valueType == nil || IsInvalidType(keyType) || IsInvalidType(valueType) {
		a.exprTypes[expr] = invalidType
		return invalidType
	}
	if dictType == nil {
		dictType = &DictType{Key: keyType, Value: valueType, SurfaceName: "dict"}
	}
	if !dictRuntimeBackedKeyType(dictType.Key) {
		a.errorf(expr.Pos(), "%s", runtimeBackedDictSupportDiagnostic(dictType))
		a.exprTypes[expr] = invalidType
		return invalidType
	}
	if !a.regionAvailableForContainer(dictType) && a.currentAllocExpr == nil {
		a.errorf(expr.Pos(), "dict literal requires an active in <arena>: scope")
	}
	a.recordAnalyzedExprType(expr, dictType)
	return dictType
}

func (a *Analyzer) analyzeSetLiteralExpr(expr *ast.ListLitExpr, expected Type) Type {
	setType, _ := contextualSetLiteralType(expected)
	var elemType Type
	if setType != nil {
		elemType = setType.Elem
	}
	for i, elem := range expr.Elems {
		et := a.analyzeValueExpr(elem, elemType)
		if elemType == nil {
			elemType = et
		} else if !IsInvalidType(et) && !AssignableTo(elemType, et) {
			a.errorf(elem.Pos(), "set literal element %d has type %s, expected %s", i, diagnosticTypeString(et), diagnosticTypeString(elemType))
		}
	}
	if elemType == nil || IsInvalidType(elemType) {
		a.exprTypes[expr] = invalidType
		return invalidType
	}
	if setType == nil {
		setType = &SetType{Elem: elemType, SurfaceName: "set"}
	}
	if !dictRuntimeBackedKeyType(setType.Elem) {
		a.errorf(expr.Pos(), "%s", runtimeBackedSetSupportDiagnostic(setType))
		a.exprTypes[expr] = invalidType
		return invalidType
	}
	if !a.regionAvailableForContainer(setType) && a.currentAllocExpr == nil {
		a.errorf(expr.Pos(), "set literal requires an active in <arena>: scope")
	}
	a.recordAnalyzedExprType(expr, setType)
	return setType
}
