package semantic

import (
	"math/big"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func derivedLoopIntegerFits(value *big.Int, typ Type) bool {
	builtin, ok := typ.(*BuiltinType)
	if !ok || value == nil {
		return false
	}
	width, unsigned := uint(0), false
	switch builtin.Name {
	case "i8":
		width = 8
	case "i16":
		width = 16
	case "i32":
		width = 32
	case "i64", "int":
		width = 64
	case "u8":
		width, unsigned = 8, true
	case "u16":
		width, unsigned = 16, true
	case "u32":
		width, unsigned = 32, true
	case "u64":
		width, unsigned = 64, true
	default:
		return false // target-dependent usize/uintptr and other representations
	}
	limit := new(big.Int).Lsh(big.NewInt(1), width)
	if unsigned {
		return value.Sign() >= 0 && value.Cmp(limit) < 0
	}
	limit.Rsh(limit, 1)
	return value.Cmp(new(big.Int).Neg(new(big.Int).Set(limit))) >= 0 && value.Cmp(limit) < 0
}

// Mathematical evaluation with a representation check at EVERY node. The
// ordinary int64 const evaluator alone cannot classify an overflowing narrow
// or unsigned range correctly. No first-iteration variable facts are consumed.
func (a *Analyzer) derivedLoopLiteralBound(expr ast.Expr, depth int, fuel *int) (*big.Int, bool) {
	if expr == nil || depth > 128 || *fuel <= 0 {
		return nil, false
	}
	*fuel--
	var value *big.Int
	switch n := expr.(type) {
	case *ast.IntLit:
		constant, ok := a.evalConstExpr(n)
		if !ok || constant.Kind != ConstInt {
			return nil, false
		}
		value = big.NewInt(constant.Int)
		if isUnsignedIntegerType(a.exprTypes[expr]) {
			value.SetUint64(uint64(constant.Int))
		}
	case *ast.ParenExpr:
		return a.derivedLoopLiteralBound(n.Inner, depth+1, fuel)
	case *ast.UnaryExpr:
		if n.LoweredCall != nil || (n.Op != lexer.TOKEN_MINUS && n.Op != lexer.TOKEN_PLUS) {
			return nil, false
		}
		operand, ok := a.derivedLoopLiteralBound(n.Operand, depth+1, fuel)
		if !ok {
			return nil, false
		}
		value = new(big.Int).Set(operand)
		if n.Op == lexer.TOKEN_MINUS {
			value.Neg(value)
		}
	case *ast.BinaryExpr:
		if n.LoweredCall != nil {
			return nil, false
		}
		left, lok := a.derivedLoopLiteralBound(n.Left, depth+1, fuel)
		right, rok := a.derivedLoopLiteralBound(n.Right, depth+1, fuel)
		if !lok || !rok {
			return nil, false
		}
		value = new(big.Int)
		switch n.Op {
		case lexer.TOKEN_PLUS:
			value.Add(left, right)
		case lexer.TOKEN_MINUS:
			value.Sub(left, right)
		case lexer.TOKEN_STAR:
			value.Mul(left, right)
		default:
			return nil, false
		}
	default:
		return nil, false
	}
	if !derivedLoopIntegerFits(value, a.exprTypes[expr]) {
		return nil, false
	}
	return value, true
}

func (a *Analyzer) classifyDerivedLiteralRange(stmt *ast.ForStmt) (known, empty, singleton bool) {
	if stmt.Op != lexer.TOKEN_RANGE_LT || stmt.Step != nil || stmt.Reverse {
		return false, false, false
	}
	fuel := 1024
	start, sk := a.derivedLoopLiteralBound(stmt.Start, 0, &fuel)
	end, ek := a.derivedLoopLiteralBound(stmt.End, 0, &fuel)
	if !sk || !ek {
		return false, false, false
	}
	common := CommonNumericType(a.exprTypes[stmt.Start], a.exprTypes[stmt.End])
	if !derivedLoopIntegerFits(start, common) || !derivedLoopIntegerFits(end, common) {
		return false, false, false
	}
	return true, start.Cmp(end) >= 0, new(big.Int).Sub(end, start).Cmp(big.NewInt(1)) == 0
}
