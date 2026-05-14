package semantic

import (
	"strconv"

	"elisacore/src/ast"
)

type indexBoundFact struct {
	Upper string
}

func cloneIndexBoundFacts(src map[string]indexBoundFact) map[string]indexBoundFact {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]indexBoundFact, len(src))
	for name, fact := range src {
		out[name] = fact
	}
	return out
}

func (a *Analyzer) indexExprRequiresUncheckedIndexPermission(expr *ast.IndexExpr) bool {
	if expr == nil {
		return false
	}
	if a.indexBoundsProven[expr] {
		return false
	}
	return indexableTypeRequiresBoundsProof(a.exprTypes[expr.Object])
}

func (a *Analyzer) markIndexBoundsProof(expr *ast.IndexExpr, objType Type) {
	if expr == nil {
		return
	}
	if expr.Fallback != nil {
		a.indexBoundsProven[expr] = true
		return
	}
	if a.indexExprHasBoundsProof(expr, objType) {
		a.indexBoundsProven[expr] = true
	}
}

func (a *Analyzer) indexExprHasBoundsProof(expr *ast.IndexExpr, objType Type) bool {
	if expr == nil {
		return false
	}
	if array, ok := stripRefForBounds(objType).(*ArrayType); ok && array != nil {
		if value, ok := a.evalConstExpr(expr.Index); ok && value.Kind == ConstInt {
			return value.Int >= 0 && (!array.HasConstSize || value.Int < array.ConstSize)
		}
	}
	indexName, ok := stripOptimizationParens(expr.Index).(*ast.Ident)
	if !ok || indexName == nil {
		return false
	}
	fact, ok := a.currentIndexBounds[indexName.Name]
	if !ok || fact.Upper == "" {
		return false
	}
	return fact.Upper == indexableUpperBoundString(expr.Object, objType)
}

func indexableTypeRequiresBoundsProof(t Type) bool {
	if IsInvalidType(t) {
		return false
	}
	if ref, ok := t.(*RefType); ok && ref != nil && (IsNumericType(ref.Elem) || IsBoolType(ref.Elem)) {
		return true
	}
	switch stripRefForBounds(t).(type) {
	case *ArrayType:
		return true
	}
	return false
}

func stripRefForBounds(t Type) Type {
	if ref, ok := t.(*RefType); ok && ref != nil && ref.State == RefStateNonNull {
		return ref.Elem
	}
	return t
}

func indexableUpperBoundString(obj ast.Expr, objType Type) string {
	base := optimizationExprString(obj)
	if base == "" {
		return ""
	}
	switch tt := stripRefForBounds(objType).(type) {
	case *ArrayType:
		if tt != nil && tt.HasConstSize {
			return strconv.FormatInt(tt.ConstSize, 10)
		}
	}
	return ""
}
