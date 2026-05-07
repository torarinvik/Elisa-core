package semantic

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) resolveArrayType(expr *ast.ArrayType) Type {
	arr := &ArrayType{Elem: a.resolveType(expr.Elem), Size: a.exprSummary(expr.Size)}
	value, ok := a.evalConstExpr(expr.Size)
	if !ok || value.Kind != ConstInt {
		if ident, identOK := expr.Size.(*ast.Ident); identOK {
			if _, paramOK := a.lookupConstParam(ident.Name); paramOK {
				arr.ConstParam = ident.Name
				return arr
			}
		}
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
		a.errorf(indexExpr.Pos(), "constant index %d out of bounds for %s", value.Int, arr)
	}
}

func (a *Analyzer) checkConstantArraySliceBounds(arr *ArrayType, startExpr ast.Expr, endExpr ast.Expr) {
	if arr == nil || !arr.HasConstSize {
		return
	}
	start, startOK := a.evalConstExpr(startExpr)
	end, endOK := a.evalConstExpr(endExpr)
	if !startOK || !endOK || start.Kind != ConstInt || end.Kind != ConstInt {
		return
	}
	if start.Int < 0 || start.Int > arr.ConstSize {
		a.errorf(startExpr.Pos(), "constant slice start %d out of bounds for %s", start.Int, arr)
	}
	if end.Int < 0 || end.Int > arr.ConstSize {
		a.errorf(endExpr.Pos(), "constant slice end %d out of bounds for %s", end.Int, arr)
	}
	if start.Int > end.Int {
		a.errorf(startExpr.Pos(), "constant slice start %d is after end %d for %s", start.Int, end.Int, arr)
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
	case *ast.FloatLit:
		value, ok := ParseFloatLiteral(n)
		if !ok {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstFloat, Float: value}, true
	case *ast.BoolLit:
		return ConstValue{Kind: ConstBool, Bool: n.Value}, true
	case *ast.StringLit:
		return ConstValue{Kind: ConstString, String: n.Value}, true
	case *ast.CharLit:
		value, ok := ParseCharLiteral(n)
		if !ok {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: value}, true
	case *ast.Ident:
		if t, ok := a.lookupConstParam(n.Name); ok {
			if valueType, ok := t.(*ConstValueType); ok && valueType != nil {
				return valueType.Value, true
			}
			return ConstValue{}, false
		}
		value, ok := a.lookupVisibleConst(n.Name)
		return value, ok
	case *ast.FieldExpr:
		ident, ok := n.Object.(*ast.Ident)
		if !ok {
			return ConstValue{}, false
		}
		for _, candidate := range a.visibleNameCandidates(ident.Name) {
			if value, ok := a.constValues[candidate+"."+n.Field]; ok {
				return value, true
			}
		}
		return ConstValue{}, false
	case *ast.ShorthandMemberExpr:
		constEnumType, ok := a.exprTypes[n].(*ConstEnumType)
		if !ok || constEnumType == nil {
			return ConstValue{}, false
		}
		member, ok := constEnumType.Member(strings.Join(n.Parts, "."))
		if !ok || member == nil {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: member.Value}, true
	case *ast.ParenExpr:
		return a.evalConstExpr(n.Inner)
	case *ast.CastExpr:
		operand, ok := a.evalConstExpr(n.Operand)
		if !ok {
			return ConstValue{}, false
		}
		return CastConstValue(operand, a.resolveType(n.Target))
	case *ast.MoveExpr:
		return a.evalConstExpr(n.Operand)
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
			switch operand.Kind {
			case ConstInt:
				return ConstValue{Kind: ConstInt, Int: -operand.Int}, true
			case ConstFloat:
				return ConstValue{Kind: ConstFloat, Float: -operand.Float}, true
			default:
				return ConstValue{}, false
			}
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
		if n.Op == lexer.TOKEN_IN {
			list, ok := a.membershipCandidateList(n.Right)
			if !ok || list == nil {
				return ConstValue{}, false
			}
			for _, elem := range list.Elems {
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
	case isConstNumeric(left) && isConstNumeric(right):
		matched = constNumericEqual(left, right)
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

func evalConstNumericBinary(op lexer.TokenKind, left, right ConstValue) (ConstValue, bool) {
	if left.Kind == ConstInt && right.Kind == ConstInt {
		return evalConstIntBinary(op, left.Int, right.Int)
	}
	if !isConstNumeric(left) || !isConstNumeric(right) {
		return ConstValue{}, false
	}
	leftFloat := constNumericAsFloat64(left)
	rightFloat := constNumericAsFloat64(right)
	switch op {
	case lexer.TOKEN_LT:
		return ConstValue{Kind: ConstBool, Bool: leftFloat < rightFloat}, true
	case lexer.TOKEN_GT:
		return ConstValue{Kind: ConstBool, Bool: leftFloat > rightFloat}, true
	case lexer.TOKEN_LTEQ:
		return ConstValue{Kind: ConstBool, Bool: leftFloat <= rightFloat}, true
	case lexer.TOKEN_GTEQ:
		return ConstValue{Kind: ConstBool, Bool: leftFloat >= rightFloat}, true
	case lexer.TOKEN_PLUS:
		return ConstValue{Kind: ConstFloat, Float: leftFloat + rightFloat}, true
	case lexer.TOKEN_MINUS:
		return ConstValue{Kind: ConstFloat, Float: leftFloat - rightFloat}, true
	case lexer.TOKEN_STAR:
		return ConstValue{Kind: ConstFloat, Float: leftFloat * rightFloat}, true
	case lexer.TOKEN_SLASH:
		if rightFloat == 0 {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstFloat, Float: leftFloat / rightFloat}, true
	default:
		return ConstValue{}, false
	}
}

func isConstNumeric(value ConstValue) bool {
	return value.Kind == ConstInt || value.Kind == ConstFloat
}

func constNumericAsFloat64(value ConstValue) float64 {
	if value.Kind == ConstFloat {
		return value.Float
	}
	return float64(value.Int)
}

func constNumericEqual(left, right ConstValue) bool {
	return math.Float64bits(constNumericAsFloat64(left)) == math.Float64bits(constNumericAsFloat64(right))
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
	if a.suppressDiagnostics {
		return
	}
	a.diagnostics = append(a.diagnostics, Diagnostic{Pos: pos, Severity: DiagnosticSeverityError, Message: fmt.Sprintf(format, formatDiagnosticArgs(args)...)})
}

func (a *Analyzer) warnf(pos lexer.Pos, format string, args ...interface{}) {
	if a.suppressDiagnostics {
		return
	}
	a.diagnostics = append(a.diagnostics, Diagnostic{Pos: pos, Severity: DiagnosticSeverityWarning, Message: fmt.Sprintf(format, formatDiagnosticArgs(args)...)})
}

func (a *Analyzer) deprecatedf(pos lexer.Pos, format string, args ...interface{}) {
	if a.suppressDiagnostics {
		return
	}
	a.diagnostics = append(a.diagnostics, Diagnostic{Pos: pos, Severity: DiagnosticSeverityDeprecated, Message: fmt.Sprintf(format, formatDiagnosticArgs(args)...)})
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

func ParseFloatLiteral(expr *ast.FloatLit) (float64, bool) {
	v, err := strconv.ParseFloat(expr.Value, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func ParseCharLiteral(expr *ast.CharLit) (int64, bool) {
	if expr == nil || len(expr.Value) != 1 {
		return 0, false
	}
	return int64(expr.Value[0]), true
}

func CastConstValue(value ConstValue, dst Type) (ConstValue, bool) {
	if storage, ok := ConstEnumStorageType(dst); ok {
		dst = storage
	}
	if idType, ok := dst.(*IDType); ok {
		dst = idType.Storage
	}
	if !IsNumericType(dst) || !isConstNumeric(value) {
		return ConstValue{}, false
	}
	if IsFloatType(dst) {
		floatValue := constNumericAsFloat64(value)
		if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
			return ConstValue{}, false
		}
		if builtin, ok := dst.(*BuiltinType); ok && builtin.Name == "f32" {
			return ConstValue{Kind: ConstFloat, Float: float64(float32(floatValue))}, true
		}
		return ConstValue{Kind: ConstFloat, Float: floatValue}, true
	}
	intValue, ok := castConstNumericToInt64(value, dst)
	if !ok {
		return ConstValue{}, false
	}
	return ConstValue{Kind: ConstInt, Int: intValue}, true
}

func castConstNumericToInt64(value ConstValue, dst Type) (int64, bool) {
	floatValue := constNumericAsFloat64(value)
	if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
		return 0, false
	}
	truncated := math.Trunc(floatValue)
	name, ok := builtinNumericTypeName(dst)
	if !ok {
		return 0, false
	}
	if isSignedConstCastBuiltin(name) {
		if usesInt64ConstRange(name) {
			// float64(math.MaxInt64) rounds up to 2^63, so compare against the exact
			// exclusive upper bound instead of float64(maxValue) to avoid accepting
			// out-of-range values like 9223372036854775808.0.
			if truncated < float64(math.MinInt64) || truncated >= math.Exp2(63) {
				return 0, false
			}
			return int64(truncated), true
		}
		minValue, maxValue, ok := signedConstCastRange(name)
		if !ok || truncated < float64(minValue) || truncated > float64(maxValue) {
			return 0, false
		}
		return narrowSignedConstCast(int64(truncated), name), true
	}
	if truncated < 0 {
		return 0, false
	}
	if usesInt64BackedUnsignedConstRange(name) {
		// The current ConstValue representation stores integers as int64, so
		// compile-time unsigned constants must stay below 2^63 to remain
		// representable without wrapping.
		if truncated >= math.Exp2(63) {
			return 0, false
		}
		return int64(truncated), true
	}
	maxValue, ok := unsignedConstCastMax(name)
	if !ok || truncated > float64(maxValue) {
		return 0, false
	}
	return int64(narrowUnsignedConstCast(uint64(truncated), name)), true
}

func builtinNumericTypeName(t Type) (string, bool) {
	if bit, ok := t.(*BitIntType); ok {
		return BitIntName(bit.Signed, bit.Width), true
	}
	if builtin, ok := t.(*BuiltinType); ok {
		return builtin.Name, true
	}
	return "", false
}

func isSignedConstCastBuiltin(name string) bool {
	if signed, _, ok := ParseBitIntName(name); ok {
		return signed
	}
	switch name {
	case "char", "int", "isize", "i8", "i16", "i32", "i64":
		return true
	default:
		return false
	}
}

func usesInt64ConstRange(name string) bool {
	if signed, width, ok := ParseBitIntName(name); ok {
		return signed && width == 64
	}
	switch name {
	case "char", "int", "isize", "i64":
		return true
	default:
		return false
	}
}

func signedConstCastRange(name string) (int64, int64, bool) {
	if signed, width, ok := ParseBitIntName(name); ok && signed {
		if width >= 64 {
			return math.MinInt64, math.MaxInt64, true
		}
		return -(int64(1) << (width - 1)), (int64(1) << (width - 1)) - 1, true
	}
	switch name {
	case "i8":
		return math.MinInt8, math.MaxInt8, true
	case "i16":
		return math.MinInt16, math.MaxInt16, true
	case "i32":
		return math.MinInt32, math.MaxInt32, true
	case "char", "int", "isize", "i64":
		return math.MinInt64, math.MaxInt64, true
	default:
		return 0, 0, false
	}
}

func unsignedConstCastMax(name string) (uint64, bool) {
	if signed, width, ok := ParseBitIntName(name); ok && !signed {
		if width >= 64 {
			return math.MaxInt64, true
		}
		return (uint64(1) << width) - 1, true
	}
	switch name {
	case "u8":
		return math.MaxUint8, true
	case "u16":
		return math.MaxUint16, true
	case "u32":
		return math.MaxUint32, true
	case "u64", "usize", "uintptr":
		return math.MaxInt64, true
	default:
		return 0, false
	}
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
