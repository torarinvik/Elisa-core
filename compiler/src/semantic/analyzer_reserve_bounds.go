package semantic

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type reserveBoundLinear struct {
	terms    map[string]int
	constant int
}

func semanticCountingLoopBoundExpr(loop *ast.ForStmt) (ast.Expr, bool) {
	if loop == nil || loop.Reverse || loop.Op != lexer.TOKEN_RANGE_LT || loop.Step != nil {
		return nil, false
	}
	if start, ok := loop.Start.(*ast.IntLit); !ok || start.Value != "0" {
		return nil, false
	}
	bound := semanticCloneReserveBoundExpr(loop.End)
	return bound, bound != nil
}

func semanticCloneReserveBoundExpr(e ast.Expr) ast.Expr {
	switch n := e.(type) {
	case *ast.Ident:
		return &ast.Ident{Position: n.Position, Name: n.Name}
	case *ast.IntLit:
		return &ast.IntLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix, IsHex: n.IsHex}
	case *ast.FieldExpr:
		obj := semanticCloneReserveBoundExpr(n.Object)
		if obj == nil {
			return nil
		}
		return &ast.FieldExpr{Position: n.Position, Object: obj, Field: n.Field}
	case *ast.BinaryExpr:
		if n.Op != lexer.TOKEN_PLUS && n.Op != lexer.TOKEN_STAR {
			return nil
		}
		left := semanticCloneReserveBoundExpr(n.Left)
		right := semanticCloneReserveBoundExpr(n.Right)
		if left == nil || right == nil {
			return nil
		}
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: left, Right: right}
	}
	return nil
}

func semanticIntReserveExpr(value int, pos lexer.Pos) ast.Expr {
	return &ast.IntLit{Position: pos, Value: strconv.Itoa(value)}
}

func semanticReserveExprIsIntOne(e ast.Expr) bool {
	lit, ok := e.(*ast.IntLit)
	return ok && lit.Value == "1"
}

func semanticIntReserveExprValue(e ast.Expr) (int, bool) {
	lit, ok := e.(*ast.IntLit)
	if !ok || lit == nil {
		return 0, false
	}
	value, err := strconv.Atoi(lit.Value)
	if err != nil {
		return 0, false
	}
	return value, true
}

func semanticAddReserveExpr(left, right ast.Expr, pos lexer.Pos) ast.Expr {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if leftValue, ok := semanticIntReserveExprValue(left); ok {
		if rightValue, ok := semanticIntReserveExprValue(right); ok {
			return semanticIntReserveExpr(leftValue+rightValue, pos)
		}
	}
	if optimizationExprString(left) == optimizationExprString(right) {
		return semanticMultiplyReserveExpr(left, semanticIntReserveExpr(2, pos), pos)
	}
	return &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: left, Right: right}
}

func semanticMultiplyReserveExpr(left, right ast.Expr, pos lexer.Pos) ast.Expr {
	if semanticReserveExprIsIntOne(left) {
		return right
	}
	if semanticReserveExprIsIntOne(right) {
		return left
	}
	if leftValue, ok := semanticIntReserveExprValue(left); ok {
		if rightValue, ok := semanticIntReserveExprValue(right); ok {
			return semanticIntReserveExpr(leftValue*rightValue, pos)
		}
	}
	return &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_STAR, Left: left, Right: right}
}

func semanticReserveBoundAtLeast(actual, expected ast.Expr) bool {
	if optimizationExprString(actual) == optimizationExprString(expected) {
		return true
	}
	actualLinear, ok := semanticReserveBoundLinearize(actual)
	if !ok {
		return false
	}
	expectedLinear, ok := semanticReserveBoundLinearize(expected)
	if !ok {
		return false
	}
	if actualLinear.constant < expectedLinear.constant {
		return false
	}
	for term, need := range expectedLinear.terms {
		if actualLinear.terms[term] < need {
			return false
		}
	}
	return true
}

func semanticReserveBoundLinearize(e ast.Expr) (reserveBoundLinear, bool) {
	switch n := e.(type) {
	case *ast.IntLit:
		value, ok := semanticIntReserveExprValue(n)
		if !ok || value < 0 {
			return reserveBoundLinear{}, false
		}
		return reserveBoundLinear{terms: map[string]int{}, constant: value}, true
	case *ast.Ident, *ast.FieldExpr:
		return reserveBoundLinear{terms: map[string]int{optimizationExprString(e): 1}}, true
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_PLUS:
			left, ok := semanticReserveBoundLinearize(n.Left)
			if !ok {
				return reserveBoundLinear{}, false
			}
			right, ok := semanticReserveBoundLinearize(n.Right)
			if !ok {
				return reserveBoundLinear{}, false
			}
			return semanticReserveBoundLinearAdd(left, right), true
		case lexer.TOKEN_STAR:
			if leftValue, ok := semanticIntReserveExprValue(n.Left); ok && leftValue >= 0 {
				right, ok := semanticReserveBoundLinearize(n.Right)
				if !ok {
					return reserveBoundLinear{}, false
				}
				return semanticReserveBoundLinearScale(right, leftValue), true
			}
			if rightValue, ok := semanticIntReserveExprValue(n.Right); ok && rightValue >= 0 {
				left, ok := semanticReserveBoundLinearize(n.Left)
				if !ok {
					return reserveBoundLinear{}, false
				}
				return semanticReserveBoundLinearScale(left, rightValue), true
			}
		}
	}
	return reserveBoundLinear{}, false
}

func semanticReserveBoundLinearAdd(left, right reserveBoundLinear) reserveBoundLinear {
	out := reserveBoundLinear{terms: map[string]int{}, constant: left.constant + right.constant}
	for term, coeff := range left.terms {
		out.terms[term] += coeff
	}
	for term, coeff := range right.terms {
		out.terms[term] += coeff
	}
	return out
}

func semanticReserveBoundLinearScale(in reserveBoundLinear, scale int) reserveBoundLinear {
	out := reserveBoundLinear{terms: map[string]int{}, constant: in.constant * scale}
	for term, coeff := range in.terms {
		out.terms[term] = coeff * scale
	}
	return out
}
