package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) canInferPackedStoreFromValue(valueExpr ast.Expr, actual Type, enumType *EnumType) bool {
	if enumType == nil || !enumType.Packed || actual == nil {
		return false
	}
	actual = StripAggregateStateType(actual)
	if viewType, ok := actual.(*PackedVariantViewType); ok && viewType != nil && viewType.Enum == enumType {
		return true
	}
	if _, _, ok := a.resolvePackedNodeStoreRoot(valueExpr, enumType); ok {
		return true
	}
	if a.valueHasExactPackedStoreIndexRoot(valueExpr, enumType) {
		return true
	}
	return false
}

func (a *Analyzer) valueHasExactPackedStoreIndexRoot(expr ast.Expr, enumType *EnumType) bool {
	if a == nil || expr == nil || enumType == nil || !enumType.Packed {
		return false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.valueHasExactPackedStoreIndexRoot(n.Inner, enumType)
	case *ast.CastExpr:
		return a.valueHasExactPackedStoreIndexRoot(n.Operand, enumType)
	case *ast.MoveExpr:
		return a.valueHasExactPackedStoreIndexRoot(n.Operand, enumType)
	case *ast.CanExpr:
		return a.valueHasExactPackedStoreIndexRoot(n.Expr, enumType)
	case *ast.Ident:
		if a.currentScope == nil {
			return false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok || sym == nil {
			return false
		}
		if a.currentValueBindings != nil {
			if valueExpr, ok := a.currentValueBindings[sym]; ok && valueExpr != nil {
				return a.valueHasExactPackedStoreIndexRoot(valueExpr, enumType)
			}
			if root := symbolAliasRoot(sym); root != nil && root != sym {
				if valueExpr, ok := a.currentValueBindings[root]; ok && valueExpr != nil {
					return a.valueHasExactPackedStoreIndexRoot(valueExpr, enumType)
				}
			}
		}
		return false
	case *ast.FieldExpr:
		if resolved, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok && resolved != nil {
			return a.valueHasExactPackedStoreIndexRoot(resolved, enumType)
		}
		return false
	case *ast.IndexExpr:
		if n.Fallback != nil {
			return false
		}
		return a.exprIsExactPackedStoreRoot(n.Object, enumType)
	default:
		return false
	}
}

func (a *Analyzer) exprIsExactPackedStoreRoot(expr ast.Expr, enumType *EnumType) bool {
	if a == nil || expr == nil || enumType == nil || !enumType.Packed {
		return false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.exprIsExactPackedStoreRoot(n.Inner, enumType)
	case *ast.CastExpr:
		return a.exprIsExactPackedStoreRoot(n.Operand, enumType)
	case *ast.MoveExpr:
		return a.exprIsExactPackedStoreRoot(n.Operand, enumType)
	case *ast.FieldExpr:
		if resolved, ok := a.resolveProjectedFieldValueExpr(n.Object, n.Field); ok && resolved != nil {
			return a.exprIsExactPackedStoreRoot(resolved, enumType)
		}
	}
	actual := a.exprTypes[expr]
	if actual == nil {
		actual = a.analyzeExpr(expr)
	}
	storeType, ok := StripAggregateStateType(actual).(*PackedEnumStoreType)
	return ok && storeType != nil && storeType.Enum == enumType
}

func (a *Analyzer) validateMoveBindStore(pos lexer.Pos, valueExpr ast.Expr, actual Type, enumType *EnumType, storeExpr ast.Expr) {
	if enumType == nil {
		return
	}
	if !enumType.Packed {
		if storeExpr != nil {
			a.errorf(storeExpr.Pos(), "ordinary enum move-as over %q does not take an in-store clause", enumType.Name)
		}
		return
	}
	if storeExpr == nil {
		if a.canInferPackedStoreFromValue(valueExpr, actual, enumType) {
			return
		}
		if _, ok := a.lookupPackedStore(enumType); ok {
			return
		}
		a.errorf(pos, "packed enum move-as over %q requires an in %s clause", enumType.Name, packedEnumStoreTypeName(enumType.Name))
		return
	}
	storeType := a.analyzeExpr(storeExpr)
	packedStore, ok := storeType.(*PackedEnumStoreType)
	if !ok {
		a.errorf(storeExpr.Pos(), "packed enum move-as over %q requires store type %q, got %s", enumType.Name, packedEnumStoreTypeName(enumType.Name), storeType)
		return
	}
	if packedStore.Enum != enumType {
		a.errorf(storeExpr.Pos(), "packed enum move-as over %q requires store type %q, got %s", enumType.Name, packedEnumStoreTypeName(enumType.Name), storeType)
	}
}
