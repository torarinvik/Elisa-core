package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) evalConstMembershipRange(value ConstValue, expr *ast.MembershipRangeExpr) (ConstValue, bool) {
	if expr == nil {
		return ConstValue{}, false
	}
	start, ok := a.evalConstExpr(expr.Start)
	if !ok {
		return ConstValue{}, false
	}
	end, ok := a.evalConstExpr(expr.End)
	if !ok {
		return ConstValue{}, false
	}
	if value.Kind != ConstInt || start.Kind != ConstInt || end.Kind != ConstInt {
		return ConstValue{}, false
	}
	inRange := value.Int >= start.Int
	if expr.Op == lexer.TOKEN_RANGE_LT {
		inRange = inRange && value.Int < end.Int
	} else {
		inRange = inRange && value.Int <= end.Int
	}
	return ConstValue{Kind: ConstBool, Bool: inRange}, true
}

func usesInt64BackedUnsignedConstRange(name string) bool {
	if signed, width, ok := ParseBitIntName(name); ok {
		return !signed && width >= 64
	}
	switch name {
	case "u64", "usize", "uintptr":
		return true
	default:
		return false
	}
}

func narrowSignedConstCast(value int64, name string) int64 {
	switch name {
	case "i8":
		return int64(int8(value))
	case "i16":
		return int64(int16(value))
	case "i32":
		return int64(int32(value))
	default:
		return value
	}
}

func narrowUnsignedConstCast(value uint64, name string) uint64 {
	switch name {
	case "u8":
		return uint64(uint8(value))
	case "u16":
		return uint64(uint16(value))
	case "u32":
		return uint64(uint32(value))
	default:
		return value
	}
}

// evalConstBinaryWithLeft finishes a binary constant fold whose left operand is already known.
func (a *Analyzer) evalConstBinaryWithLeft(n *ast.BinaryExpr, left ConstValue) (ConstValue, bool) {
	if n.Op == lexer.TOKEN_IN {
		list, ok := a.membershipCandidateList(n.Right)
		if !ok || list == nil {
			return ConstValue{}, false
		}
		for _, elem := range list.Elems {
			if rangeExpr, ok := elem.(*ast.MembershipRangeExpr); ok {
				matched, ok := a.evalConstMembershipRange(left, rangeExpr)
				if !ok {
					return ConstValue{}, false
				}
				if matched.Bool {
					return matched, true
				}
				continue
			}
			candidate, ok := a.evalConstExpr(elem)
			if !ok {
				return ConstValue{}, false
			}
			matched, ok := a.evalConstEquality(left, candidate, true)
			if !ok {
				return ConstValue{}, false
			}
			if matched.Bool {
				return matched, true
			}
		}
		return ConstValue{Kind: ConstBool, Bool: false}, true
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
		if result, ok := evalConstNumericBinary(n.Op, left, right); ok {
			return result, true
		}
		return ConstValue{}, false
	default:
		return ConstValue{}, false
	}
}
