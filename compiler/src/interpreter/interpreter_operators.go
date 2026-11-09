package interpreter

import (
	"elisacore/src/lexer"
	"fmt"
)

func evalUnaryOp(op lexer.TokenKind, operand Value) (Value, error) {
	operand = derefValue(operand)
	switch op {
	case lexer.TOKEN_NOT:
		value, err := requireBool(operand)
		if err != nil {
			return VoidValue(), err
		}
		return BoolValue(!value), nil
	case lexer.TOKEN_MINUS:
		switch operand.kind {
		case valueInt:
			return IntValue(-operand.int64Val), nil
		case valueFloat:
			return FloatValue(-operand.floatVal), nil
		default:
			return VoidValue(), fmt.Errorf("unary - requires numeric operand, got %s", operand.String())
		}
	case lexer.TOKEN_PLUS:
		switch operand.kind {
		case valueInt, valueFloat:
			return operand, nil
		default:
			return VoidValue(), fmt.Errorf("unary + requires numeric operand, got %s", operand.String())
		}
	case lexer.TOKEN_TILDE:
		value, err := requireInt(operand)
		if err != nil {
			return VoidValue(), err
		}
		return IntValue(^value), nil
	default:
		return VoidValue(), fmt.Errorf("unsupported unary operator %s", lexer.TokenName(op))
	}
}
func augAssignBaseOp(op lexer.TokenKind) lexer.TokenKind {
	switch op {
	case lexer.TOKEN_PLUSEQ:
		return lexer.TOKEN_PLUS
	case lexer.TOKEN_MINUSEQ:
		return lexer.TOKEN_MINUS
	case lexer.TOKEN_STAREQ:
		return lexer.TOKEN_STAR
	case lexer.TOKEN_SLASHEQ:
		return lexer.TOKEN_SLASH
	case lexer.TOKEN_PERCENTEQ:
		return lexer.TOKEN_PERCENT
	case lexer.TOKEN_CARETEQ:
		return lexer.TOKEN_CARET
	case lexer.TOKEN_PIPEEQ:
		return lexer.TOKEN_PIPE
	case lexer.TOKEN_AMPEQ:
		return lexer.TOKEN_AMPERSAND
	case lexer.TOKEN_LSHIFTEQ:
		return lexer.TOKEN_LSHIFT
	case lexer.TOKEN_RSHIFTEQ:
		return lexer.TOKEN_RSHIFT
	default:
		return op
	}
}
func evalBinaryOp(op lexer.TokenKind, left Value, right Value) (Value, error) {
	left = derefValue(left)
	right = derefValue(right)
	switch op {
	case lexer.TOKEN_AND:
		l, err := requireBool(left)
		if err != nil {
			return VoidValue(), err
		}
		r, err := requireBool(right)
		if err != nil {
			return VoidValue(), err
		}
		return BoolValue(l && r), nil
	case lexer.TOKEN_OR:
		l, err := requireBool(left)
		if err != nil {
			return VoidValue(), err
		}
		r, err := requireBool(right)
		if err != nil {
			return VoidValue(), err
		}
		return BoolValue(l || r), nil
	case lexer.TOKEN_EQEQ:
		return BoolValue(valuesEqual(left, right)), nil
	case lexer.TOKEN_BANGEQ:
		return BoolValue(!valuesEqual(left, right)), nil
	case lexer.TOKEN_PLUS:
		if left.kind == valueString || right.kind == valueString {
			leftText, err := stringifyValue(left)
			if err != nil {
				return VoidValue(), err
			}
			rightText, err := stringifyValue(right)
			if err != nil {
				return VoidValue(), err
			}
			return StringValue(leftText + rightText), nil
		}
	}
	if left.kind == valueFloat || right.kind == valueFloat {
		return evalFloatBinaryOp(op, left, right)
	}
	leftInt, err := requireInt(left)
	if err != nil {
		return VoidValue(), err
	}
	rightInt, err := requireInt(right)
	if err != nil {
		return VoidValue(), err
	}
	switch op {
	case lexer.TOKEN_PLUS:
		return IntValue(leftInt + rightInt), nil
	case lexer.TOKEN_MINUS:
		return IntValue(leftInt - rightInt), nil
	case lexer.TOKEN_STAR:
		return IntValue(leftInt * rightInt), nil
	case lexer.TOKEN_SLASH:
		if rightInt == 0 {
			return VoidValue(), fmt.Errorf("division by zero")
		}
		return IntValue(leftInt / rightInt), nil
	case lexer.TOKEN_PERCENT:
		if rightInt == 0 {
			return VoidValue(), fmt.Errorf("modulo by zero")
		}
		return IntValue(leftInt % rightInt), nil
	case lexer.TOKEN_LT:
		return BoolValue(leftInt < rightInt), nil
	case lexer.TOKEN_GT:
		return BoolValue(leftInt > rightInt), nil
	case lexer.TOKEN_LTEQ:
		return BoolValue(leftInt <= rightInt), nil
	case lexer.TOKEN_GTEQ:
		return BoolValue(leftInt >= rightInt), nil
	case lexer.TOKEN_PIPE:
		return IntValue(leftInt | rightInt), nil
	case lexer.TOKEN_CARET:
		return IntValue(leftInt ^ rightInt), nil
	case lexer.TOKEN_AMPERSAND:
		return IntValue(leftInt & rightInt), nil
	case lexer.TOKEN_LSHIFT:
		return IntValue(leftInt << rightInt), nil
	case lexer.TOKEN_RSHIFT:
		return IntValue(leftInt >> rightInt), nil
	default:
		return VoidValue(), fmt.Errorf("unsupported binary operator %s", lexer.TokenName(op))
	}
}
func evalFloatBinaryOp(op lexer.TokenKind, left Value, right Value) (Value, error) {
	leftFloat, err := numericAsFloat(left)
	if err != nil {
		return VoidValue(), err
	}
	rightFloat, err := numericAsFloat(right)
	if err != nil {
		return VoidValue(), err
	}
	switch op {
	case lexer.TOKEN_PLUS:
		return FloatValue(leftFloat + rightFloat), nil
	case lexer.TOKEN_MINUS:
		return FloatValue(leftFloat - rightFloat), nil
	case lexer.TOKEN_STAR:
		return FloatValue(leftFloat * rightFloat), nil
	case lexer.TOKEN_SLASH:
		if rightFloat == 0 {
			return VoidValue(), fmt.Errorf("division by zero")
		}
		return FloatValue(leftFloat / rightFloat), nil
	case lexer.TOKEN_LT:
		return BoolValue(leftFloat < rightFloat), nil
	case lexer.TOKEN_GT:
		return BoolValue(leftFloat > rightFloat), nil
	case lexer.TOKEN_LTEQ:
		return BoolValue(leftFloat <= rightFloat), nil
	case lexer.TOKEN_GTEQ:
		return BoolValue(leftFloat >= rightFloat), nil
	default:
		return VoidValue(), fmt.Errorf("unsupported float binary operator %s", lexer.TokenName(op))
	}
}
func requireBool(value Value) (bool, error) {
	value = derefValue(value)
	if value.kind != valueBool {
		return false, fmt.Errorf("expected bool, got %s", value.String())
	}
	return value.boolVal, nil
}
func requireInt(value Value) (int64, error) {
	value = derefValue(value)
	if value.kind != valueInt {
		return 0, fmt.Errorf("expected integer, got %s", value.String())
	}
	return value.int64Val, nil
}
func numericAsFloat(value Value) (float64, error) {
	switch value.kind {
	case valueFloat:
		return value.floatVal, nil
	case valueInt:
		return float64(value.int64Val), nil
	default:
		return 0, fmt.Errorf("expected numeric value, got %s", value.String())
	}
}
