package semantic

import (
	"fmt"
	"strconv"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) resolveArrayType(expr *ast.ArrayType) Type {
	arr := &ArrayType{Elem: a.resolveType(expr.Elem), Size: a.exprSummary(expr.Size)}
	value, ok := a.evalConstExpr(expr.Size)
	if !ok || value.Kind != ConstInt {
		a.errorf(expr.Size.Pos(), "array size must be a compile-time integer")
		return arr
	}
	if value.Int < 0 {
		a.errorf(expr.Size.Pos(), "array size must be non-negative, got %d", value.Int)
		return arr
	}
	arr.HasConstSize = true
	arr.ConstSize = value.Int
	return arr
}

func (a *Analyzer) checkConstantArrayIndexBounds(arr *ArrayType, indexExpr ast.Expr) {
	if arr == nil || !arr.HasConstSize {
		return
	}
	value, ok := a.evalConstExpr(indexExpr)
	if !ok || value.Kind != ConstInt {
		return
	}
	if value.Int < 0 || value.Int >= arr.ConstSize {
		a.errorf(indexExpr.Pos(), "constant index %d out of bounds for %s", value.Int, arr.String())
	}
}

func (a *Analyzer) evalConstBoolExpr(expr ast.Expr) (bool, bool) {
	value, ok := a.evalConstExpr(expr)
	if !ok || value.Kind != ConstBool {
		return false, false
	}
	return value.Bool, true
}

func (a *Analyzer) evalConstStringExpr(expr ast.Expr) (string, bool) {
	value, ok := a.evalConstExpr(expr)
	if !ok || value.Kind != ConstString {
		return "", false
	}
	return value.String, true
}

func (a *Analyzer) evalConstExpr(expr ast.Expr) (ConstValue, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		value, ok := ParseIntLiteral(n)
		if !ok {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: value}, true
	case *ast.BoolLit:
		return ConstValue{Kind: ConstBool, Bool: n.Value}, true
	case *ast.StringLit:
		return ConstValue{Kind: ConstString, String: n.Value}, true
	case *ast.Ident:
		value, ok := a.constValues[n.Name]
		return value, ok
	case *ast.ParenExpr:
		return a.evalConstExpr(n.Inner)
	case *ast.UnaryExpr:
		operand, ok := a.evalConstExpr(n.Operand)
		if !ok {
			return ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_NOT:
			if operand.Kind != ConstBool {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstBool, Bool: !operand.Bool}, true
		case lexer.TOKEN_MINUS:
			if operand.Kind != ConstInt {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstInt, Int: -operand.Int}, true
		case lexer.TOKEN_TILDE:
			if operand.Kind != ConstInt {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstInt, Int: ^operand.Int}, true
		default:
			return ConstValue{}, false
		}
	case *ast.BinaryExpr:
		left, ok := a.evalConstExpr(n.Left)
		if !ok {
			return ConstValue{}, false
		}
		right, ok := a.evalConstExpr(n.Right)
		if !ok {
			return ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_AND:
			if left.Kind != ConstBool || right.Kind != ConstBool {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstBool, Bool: left.Bool && right.Bool}, true
		case lexer.TOKEN_OR:
			if left.Kind != ConstBool || right.Kind != ConstBool {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstBool, Bool: left.Bool || right.Bool}, true
		case lexer.TOKEN_EQEQ:
			return a.evalConstEquality(left, right, true)
		case lexer.TOKEN_BANGEQ:
			return a.evalConstEquality(left, right, false)
		case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ,
			lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT,
			lexer.TOKEN_CARET, lexer.TOKEN_PIPE, lexer.TOKEN_AMPERSAND,
			lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
			if left.Kind != ConstInt || right.Kind != ConstInt {
				return ConstValue{}, false
			}
			return evalConstIntBinary(n.Op, left.Int, right.Int)
		default:
			return ConstValue{}, false
		}
	case *ast.TernaryExpr:
		cond, ok := a.evalConstBoolExpr(n.Cond)
		if !ok {
			return ConstValue{}, false
		}
		if cond {
			return a.evalConstExpr(n.Value)
		}
		return a.evalConstExpr(n.Alt)
	default:
		return ConstValue{}, false
	}
}

func (a *Analyzer) evalConstEquality(left, right ConstValue, equal bool) (ConstValue, bool) {
	matched := false
	switch {
	case left.Kind == ConstInt && right.Kind == ConstInt:
		matched = left.Int == right.Int
	case left.Kind == ConstBool && right.Kind == ConstBool:
		matched = left.Bool == right.Bool
	case left.Kind == ConstString && right.Kind == ConstString:
		matched = left.String == right.String
	default:
		return ConstValue{}, false
	}
	if !equal {
		matched = !matched
	}
	return ConstValue{Kind: ConstBool, Bool: matched}, true
}

func evalConstIntBinary(op lexer.TokenKind, left, right int64) (ConstValue, bool) {
	switch op {
	case lexer.TOKEN_LT:
		return ConstValue{Kind: ConstBool, Bool: left < right}, true
	case lexer.TOKEN_GT:
		return ConstValue{Kind: ConstBool, Bool: left > right}, true
	case lexer.TOKEN_LTEQ:
		return ConstValue{Kind: ConstBool, Bool: left <= right}, true
	case lexer.TOKEN_GTEQ:
		return ConstValue{Kind: ConstBool, Bool: left >= right}, true
	case lexer.TOKEN_PLUS:
		return ConstValue{Kind: ConstInt, Int: left + right}, true
	case lexer.TOKEN_MINUS:
		return ConstValue{Kind: ConstInt, Int: left - right}, true
	case lexer.TOKEN_STAR:
		return ConstValue{Kind: ConstInt, Int: left * right}, true
	case lexer.TOKEN_SLASH:
		if right == 0 {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: left / right}, true
	case lexer.TOKEN_PERCENT:
		if right == 0 {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: left % right}, true
	case lexer.TOKEN_CARET:
		return ConstValue{Kind: ConstInt, Int: left ^ right}, true
	case lexer.TOKEN_PIPE:
		return ConstValue{Kind: ConstInt, Int: left | right}, true
	case lexer.TOKEN_AMPERSAND:
		return ConstValue{Kind: ConstInt, Int: left & right}, true
	case lexer.TOKEN_LSHIFT:
		return ConstValue{Kind: ConstInt, Int: left << right}, true
	case lexer.TOKEN_RSHIFT:
		return ConstValue{Kind: ConstInt, Int: left >> right}, true
	default:
		return ConstValue{}, false
	}
}

func (a *Analyzer) errorf(pos lexer.Pos, format string, args ...interface{}) {
	a.diagnostics = append(a.diagnostics, Diagnostic{Pos: pos, Message: fmt.Sprintf(format, args...)})
}

func isNullableRef(t Type) bool {
	r, ok := t.(*RefType)
	return ok && r.State == RefStateNullable
}

func isRefLike(t Type) bool {
	_, ok := t.(*RefType)
	return ok
}

func ParseIntLiteral(expr *ast.IntLit) (int64, bool) {
	base := 10
	text := expr.Value
	if expr.IsHex {
		base = 0
	}
	v, err := strconv.ParseInt(text, base, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
