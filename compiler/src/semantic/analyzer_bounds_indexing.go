package semantic

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
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

func (a *Analyzer) applyIndexBoundsFactsForCondition(cond ast.Expr, truthy bool) {
	facts := indexBoundsFactsForCondition(cond, truthy)
	if len(facts) == 0 {
		return
	}
	if a.currentIndexBounds == nil {
		a.currentIndexBounds = make(map[string]indexBoundFact, len(facts))
	}
	for name, fact := range facts {
		if fact.Upper == "" {
			continue
		}
		a.currentIndexBounds[name] = fact
	}
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
	case *ArrayType, *DArrayType, *DArrayViewType, *ViewType, *DStrType, *SViewType:
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
	case *DArrayType:
		return base + ".count"
	case *DArrayViewType, *ViewType, *DStrType, *SViewType:
		return base + ".len"
	}
	return ""
}

func indexBoundsFactsForCondition(cond ast.Expr, truthy bool) map[string]indexBoundFact {
	facts := make(map[string]indexBoundFact)
	collectIndexBoundsFacts(cond, truthy, facts)
	if len(facts) == 0 {
		return nil
	}
	return facts
}

func collectIndexBoundsFacts(cond ast.Expr, truthy bool, facts map[string]indexBoundFact) {
	switch n := stripOptimizationParens(cond).(type) {
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			collectIndexBoundsFacts(n.Operand, !truthy, facts)
		}
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			if truthy {
				collectIndexBoundsFacts(n.Left, true, facts)
				collectIndexBoundsFacts(n.Right, true, facts)
			}
		case lexer.TOKEN_OR:
			if !truthy {
				collectIndexBoundsFacts(n.Left, false, facts)
				collectIndexBoundsFacts(n.Right, false, facts)
			}
		case lexer.TOKEN_LT:
			if truthy {
				recordStrictUpperBoundFact(n.Left, n.Right, facts)
			}
		case lexer.TOKEN_GT:
			if truthy {
				recordStrictUpperBoundFact(n.Right, n.Left, facts)
			}
		case lexer.TOKEN_GTEQ:
			if !truthy {
				recordStrictUpperBoundFact(n.Left, n.Right, facts)
			}
		case lexer.TOKEN_LTEQ:
			if !truthy {
				recordStrictUpperBoundFact(n.Right, n.Left, facts)
			}
		}
	}
}

func recordStrictUpperBoundFact(index ast.Expr, upper ast.Expr, facts map[string]indexBoundFact) {
	name, ok := boundIndexName(index)
	if !ok {
		return
	}
	upperString := optimizationExprString(upper)
	if upperString == "" {
		return
	}
	facts[name] = indexBoundFact{Upper: upperString}
}

func boundIndexName(expr ast.Expr) (string, bool) {
	ident, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok || ident == nil || ident.Name == "" {
		return "", false
	}
	return ident.Name, true
}
