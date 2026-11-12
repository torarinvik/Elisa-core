package reservebounds

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/exprkey"
	"elisacore/src/lexer"
)

type reserveBoundLinear struct {
	terms    map[string]int
	constant int
}

// Only compare keys for forms whose complete syntax the key renderer retains.
func stableReserveExpr(e ast.Expr) bool {
	switch n := e.(type) {
	case *ast.Ident:
		return n != nil && n.Name != ""
	case *ast.IntLit:
		return n != nil && n.Value != ""
	case *ast.FieldExpr:
		return n != nil && !n.Safe && n.Field != "" && stableReserveExpr(n.Object)
	case *ast.BinaryExpr:
		return n != nil && n.LoweredCall == nil && (n.Op == lexer.TOKEN_PLUS || n.Op == lexer.TOKEN_STAR) && stableReserveExpr(n.Left) && stableReserveExpr(n.Right)
	default:
		return false
	}
}

func CountingLoopBoundExpr(loop *ast.ForStmt) (ast.Expr, bool) {
	if loop == nil || loop.Reverse || loop.Op != lexer.TOKEN_RANGE_LT || loop.Step != nil {
		return nil, false
	}
	if start, ok := loop.Start.(*ast.IntLit); !ok || start.Value != "0" {
		return nil, false
	}
	bound := CloneReserveBoundExpr(loop.End)
	return bound, bound != nil
}

func CloneReserveBoundExpr(e ast.Expr) ast.Expr {
	switch n := e.(type) {
	case *ast.Ident:
		return &ast.Ident{Position: n.Position, Name: n.Name}
	case *ast.IntLit:
		return &ast.IntLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix, IsHex: n.IsHex}
	case *ast.FieldExpr:
		obj := CloneReserveBoundExpr(n.Object)
		if obj == nil {
			return nil
		}
		return &ast.FieldExpr{Position: n.Position, Object: obj, Field: n.Field}
	case *ast.BinaryExpr:
		if n.Op != lexer.TOKEN_PLUS && n.Op != lexer.TOKEN_STAR {
			return nil
		}
		left := CloneReserveBoundExpr(n.Left)
		right := CloneReserveBoundExpr(n.Right)
		if left == nil || right == nil {
			return nil
		}
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: left, Right: right}
	}
	return nil
}

func IntReserveExpr(value int, pos lexer.Pos) ast.Expr {
	return &ast.IntLit{Position: pos, Value: strconv.Itoa(value)}
}

func ReserveExprIsIntOne(e ast.Expr) bool {
	lit, ok := e.(*ast.IntLit)
	return ok && lit.Value == "1"
}

func IntReserveExprValue(e ast.Expr) (int, bool) {
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

func AddReserveExpr(left, right ast.Expr, pos lexer.Pos) ast.Expr {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if leftValue, ok := IntReserveExprValue(left); ok {
		if rightValue, ok := IntReserveExprValue(right); ok {
			return IntReserveExpr(leftValue+rightValue, pos)
		}
	}
	if stableReserveExpr(left) && stableReserveExpr(right) && exprkey.String(left) == exprkey.String(right) {
		return MultiplyReserveExpr(left, IntReserveExpr(2, pos), pos)
	}
	return &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: left, Right: right}
}

func MultiplyReserveExpr(left, right ast.Expr, pos lexer.Pos) ast.Expr {
	if ReserveExprIsIntOne(left) {
		return right
	}
	if ReserveExprIsIntOne(right) {
		return left
	}
	if leftValue, ok := IntReserveExprValue(left); ok {
		if rightValue, ok := IntReserveExprValue(right); ok {
			return IntReserveExpr(leftValue*rightValue, pos)
		}
	}
	return &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_STAR, Left: left, Right: right}
}

func ReserveBoundAtLeast(actual, expected ast.Expr) bool {
	if stableReserveExpr(actual) && stableReserveExpr(expected) && exprkey.String(actual) == exprkey.String(expected) {
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
		value, ok := IntReserveExprValue(n)
		if !ok || value < 0 {
			return reserveBoundLinear{}, false
		}
		return reserveBoundLinear{terms: map[string]int{}, constant: value}, true
	case *ast.Ident, *ast.FieldExpr:
		if !stableReserveExpr(e) {
			return reserveBoundLinear{}, false
		}
		return reserveBoundLinear{terms: map[string]int{exprkey.String(e): 1}}, true
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
			if leftValue, ok := IntReserveExprValue(n.Left); ok && leftValue >= 0 {
				right, ok := semanticReserveBoundLinearize(n.Right)
				if !ok {
					return reserveBoundLinear{}, false
				}
				return semanticReserveBoundLinearScale(right, leftValue), true
			}
			if rightValue, ok := IntReserveExprValue(n.Right); ok && rightValue >= 0 {
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
