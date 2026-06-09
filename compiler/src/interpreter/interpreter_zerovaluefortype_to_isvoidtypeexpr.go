package interpreter

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
	"fmt"
	"strconv"
	"strings"
)

func (i *Interpreter) zeroValueForType(typ ast.TypeExpr) (Value, error) {
	switch n := typ.(type) {
	case nil:
		return VoidValue(), nil
	case *ast.NamedType:
		switch n.Name {
		case "void":
			return VoidValue(), nil
		case "bool":
			return BoolValue(false), nil
		case "f32", "f64":
			return FloatValue(0), nil
		case "Arena":
			return StructInstanceValue("Arena", []string{"begin", "end", "end_index"}, map[string]Value{
				"begin":     NullValue(),
				"end":       NullValue(),
				"end_index": IntValue(0),
			}), nil
		case "ArenaMark":
			return StructInstanceValue("ArenaMark", []string{"region", "count"}, map[string]Value{
				"region": NullValue(),
				"count":  IntValue(0),
			}), nil
		case "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr":
			return IntValue(0), nil
		default:
			if decl, ok := i.lookupStructDecl(n.Name); ok && decl != nil {
				fields := make(map[string]Value, len(decl.Fields))
				order := make([]string, 0, len(decl.Fields))
				for _, field := range decl.Fields {
					zero, err := i.zeroValueForType(field.Type)
					if err != nil {
						return VoidValue(), err
					}
					fields[field.Name] = zero
					order = append(order, field.Name)
				}
				return StructInstanceValue(decl.Name, order, fields), nil
			}
			if i != nil && i.result != nil && i.result.NamedTypes != nil {
				if resolved, ok := i.result.NamedTypes[n.Name]; ok {
					return i.zeroValueForSemanticType(resolved)
				}
			}
			return VoidValue(), fmt.Errorf("no zero value rule for type %q", n.Name)
		}
	case *ast.BuiltinTypeExpr:
		switch n.Name {
		case "array":
			if len(n.TypeArgs) == 0 || len(n.ValueArgs) == 0 {
				return VoidValue(), fmt.Errorf("array zero-initialization requires element type and size")
			}
			size, err := i.constArraySize(n.ValueArgs[0])
			if err != nil {
				return VoidValue(), err
			}
			values := make([]Value, size)
			for idx := 0; idx < size; idx++ {
				zero, err := i.zeroValueForType(n.TypeArgs[0])
				if err != nil {
					return VoidValue(), err
				}
				values[idx] = zero
			}
			return ListValue(values), nil
		case "darray":
			return ListValue(nil), nil
		default:
			return i.zeroValueForType(&ast.NamedType{Position: n.Position, Name: n.Name})
		}
	case *ast.MutableType:
		return i.zeroValueForType(n.Elem)
	case *ast.TailType:
		return i.zeroValueForType(n.Elem)
	case *ast.OptionalTypeExpr:
		return NullValue(), nil
	case *ast.RefType:
		return NullValue(), nil
	case *ast.ArrayType:
		size, err := i.constArraySize(n.Size)
		if err != nil {
			return VoidValue(), err
		}
		values := make([]Value, size)
		for idx := 0; idx < size; idx++ {
			zero, err := i.zeroValueForType(n.Elem)
			if err != nil {
				return VoidValue(), err
			}
			values[idx] = zero
		}
		return ListValue(values), nil
	default:
		return VoidValue(), fmt.Errorf("no zero value rule for type %T", typ)
	}
}
func (i *Interpreter) zeroValueForSemanticType(typ semantic.Type) (Value, error) {
	switch t := typ.(type) {
	case nil:
		return VoidValue(), fmt.Errorf("zeroed literal has no resolved semantic type")
	case *semantic.BuiltinType:
		return i.zeroValueForType(&ast.NamedType{Name: t.Name})
	case *semantic.BitIntType, *semantic.EnumType, *semantic.ConstEnumType:
		return IntValue(0), nil
	case *semantic.RefType, *semantic.OptionalType, *semantic.NullType:
		return NullValue(), nil
	case *semantic.ArrayType:
		if !t.HasConstSize || t.ConstSize < 0 {
			return VoidValue(), fmt.Errorf("zeroed array requires a constant non-negative size")
		}
		values := make([]Value, int(t.ConstSize))
		for idx := range values {
			zero, err := i.zeroValueForSemanticType(t.Elem)
			if err != nil {
				return VoidValue(), err
			}
			values[idx] = zero
		}
		return ListValue(values), nil
	case *semantic.DArrayType:
		return ListValue(nil), nil
	case *semantic.StructType:
		if t.Decl != nil {
			fields := make(map[string]Value, len(t.Decl.Fields))
			order := make([]string, 0, len(t.Decl.Fields))
			for _, fieldDecl := range t.Decl.Fields {
				fieldType, ok := t.Fields[fieldDecl.Name]
				if !ok {
					return VoidValue(), fmt.Errorf("struct %s has no resolved field %q", t.Name, fieldDecl.Name)
				}
				zero, err := i.zeroValueForSemanticType(fieldType.Type)
				if err != nil {
					return VoidValue(), err
				}
				fields[fieldDecl.Name] = zero
				order = append(order, fieldDecl.Name)
			}
			return StructInstanceValue(t.Name, order, fields), nil
		}
		return i.zeroValueForType(&ast.NamedType{Name: t.Name})
	default:
		return VoidValue(), fmt.Errorf("no zero value rule for semantic type %T (%s)", typ, typ.String())
	}
}
func (i *Interpreter) constArraySize(expr ast.Expr) (int, error) {
	var value Value
	switch n := expr.(type) {
	case *ast.IntLit:
		parsed, err := parseIntLiteral(n)
		if err != nil {
			return 0, err
		}
		value = parsed
	case *ast.Ident:
		resolved, err := i.lookupValue(nil, n.Name)
		if err != nil {
			return 0, fmt.Errorf("array zero-initialization requires a constant integer size: %w", err)
		}
		value = resolved
	default:
		return 0, fmt.Errorf("array zero-initialization requires a constant integer size, got %T", expr)
	}
	if value.kind != valueInt || value.int64Val < 0 {
		return 0, fmt.Errorf("invalid array size %s", value.String())
	}
	return int(value.int64Val), nil
}
func (i *Interpreter) castValue(value Value, target ast.TypeExpr) (Value, error) {
	switch n := target.(type) {
	case *ast.RefType, *ast.OptionalTypeExpr:
		return value, nil
	case *ast.MutableType:
		return i.castValue(value, n.Elem)
	case *ast.TailType:
		return i.castValue(value, n.Elem)
	}
	value = derefValue(value)
	switch n := target.(type) {
	case *ast.NamedType:
		switch n.Name {
		case "void":
			return VoidValue(), nil
		case "bool":
			switch value.kind {
			case valueBool:
				return value, nil
			case valueInt:
				return BoolValue(value.int64Val != 0), nil
			case valueNull:
				return BoolValue(false), nil
			default:
				return VoidValue(), fmt.Errorf("cannot cast %s to bool", value.String())
			}
		case "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr":
			switch value.kind {
			case valueInt:
				return value, nil
			case valueBool:
				if value.boolVal {
					return IntValue(1), nil
				}
				return IntValue(0), nil
			case valueFloat:
				return IntValue(int64(value.floatVal)), nil
			case valueNull:
				return IntValue(0), nil
			default:
				return VoidValue(), fmt.Errorf("cannot cast %s to %s", value.String(), n.Name)
			}
		case "f32", "f64":
			switch value.kind {
			case valueFloat:
				return value, nil
			case valueInt:
				return FloatValue(float64(value.int64Val)), nil
			case valueBool:
				if value.boolVal {
					return FloatValue(1), nil
				}
				return FloatValue(0), nil
			default:
				return VoidValue(), fmt.Errorf("cannot cast %s to %s", value.String(), n.Name)
			}
		default:
			if value.kind == valueStruct && value.structVal != nil && value.structVal.Name == n.Name {
				return value, nil
			}
			if value.kind == valueString {
				return value, nil
			}
			return value, nil
		}
	default:
		return value, nil
	}
}
func stringifyOrWrap(value Value) (Value, error) {
	text, err := stringifyValue(value)
	if err != nil {
		return VoidValue(), err
	}
	return StringValue(text), nil
}
func parseIntLiteral(lit *ast.IntLit) (Value, error) {
	if lit == nil {
		return VoidValue(), fmt.Errorf("integer literal is nil")
	}
	base := 10
	text := lit.Value
	if lit.IsHex {
		base = 0
	}
	value, err := strconv.ParseInt(text, base, 64)
	if err != nil {
		// A literal that overflows int64 but fits uint64 -- a high address or a
		// full-width mask such as 0xFFFFFFFF00000000 -- is valid in u64/uintptr
		// contexts. Reinterpret its bits as int64 so the value round-trips.
		if uvalue, uerr := strconv.ParseUint(text, base, 64); uerr == nil {
			return IntValue(int64(uvalue)), nil
		}
		return VoidValue(), err
	}
	return IntValue(value), nil
}
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
func indexValue(value Value, index int64) (Value, error) {
	return indexValueAt(value, index)
}
func valueSequenceLength(value Value) (int64, error) {
	value = derefValue(value)
	switch value.kind {
	case valueList:
		return int64(len(value.listVal)), nil
	case valueString:
		return int64(len(value.strVal)), nil
	default:
		return 0, fmt.Errorf("slicing requires list or string, got %s", value.String())
	}
}

func indexInRange(value Value, index int64) (bool, error) {
	value = derefValue(value)
	if index < 0 {
		return false, nil
	}
	switch value.kind {
	case valueList:
		return int(index) < len(value.listVal), nil
	case valueString:
		return int(index) < len(value.strVal), nil
	default:
		return false, fmt.Errorf("indexing requires list or string, got %s", value.String())
	}
}
func indexValueAt(value Value, index int64) (Value, error) {
	value = derefValue(value)
	if index < 0 {
		return VoidValue(), fmt.Errorf("index %d out of range", index)
	}
	switch value.kind {
	case valueList:
		if int(index) >= len(value.listVal) {
			return VoidValue(), fmt.Errorf("index %d out of range", index)
		}
		return value.listVal[index].Clone(), nil
	case valueString:
		if int(index) >= len(value.strVal) {
			return VoidValue(), fmt.Errorf("index %d out of range", index)
		}
		return IntValue(int64(value.strVal[index])), nil
	default:
		return VoidValue(), fmt.Errorf("indexing requires list or string, got %s", value.String())
	}
}
func sliceValue(value Value, start, end int64) (Value, error) {
	value = derefValue(value)
	if start < 0 || end < start {
		return VoidValue(), fmt.Errorf("invalid slice [%d:%d]", start, end)
	}
	switch value.kind {
	case valueList:
		if int(end) > len(value.listVal) {
			return VoidValue(), fmt.Errorf("slice end %d out of range", end)
		}
		return ListValue(value.listVal[start:end]), nil
	case valueString:
		if int(end) > len(value.strVal) {
			return VoidValue(), fmt.Errorf("slice end %d out of range", end)
		}
		return StringValue(value.strVal[start:end]), nil
	default:
		return VoidValue(), fmt.Errorf("slicing requires list or string, got %s", value.String())
	}
}
func valuesEqual(left, right Value) bool {
	if left.kind == valueNull || right.kind == valueNull {
		return left.kind == right.kind
	}
	if left.kind == valueFloat || right.kind == valueFloat {
		lf, err := numericAsFloat(left)
		if err != nil {
			return false
		}
		rf, err := numericAsFloat(right)
		if err != nil {
			return false
		}
		return lf == rf
	}
	if left.kind != right.kind {
		return false
	}
	switch left.kind {
	case valueVoid:
		return true
	case valueInt:
		return left.int64Val == right.int64Val
	case valueBool:
		return left.boolVal == right.boolVal
	case valueString:
		return left.strVal == right.strVal
	case valueList:
		if len(left.listVal) != len(right.listVal) {
			return false
		}
		for i := range left.listVal {
			if !valuesEqual(left.listVal[i], right.listVal[i]) {
				return false
			}
		}
		return true
	case valueStruct:
		if left.structVal == nil || right.structVal == nil || left.structVal.Name != right.structVal.Name {
			return false
		}
		if len(left.structVal.Fields) != len(right.structVal.Fields) {
			return false
		}
		for name, value := range left.structVal.Fields {
			if !valuesEqual(value, right.structVal.Fields[name]) {
				return false
			}
		}
		return true
	case valueFunction:
		if left.lambdaVal != nil || right.lambdaVal != nil {
			return left.lambdaVal != nil && right.lambdaVal != nil && left.lambdaVal.id == right.lambdaVal.id
		}
		return left.funcName == right.funcName
	default:
		return false
	}
}
func annotateRuntimeError(pos lexer.Pos, err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if strings.HasPrefix(message, pos.String()+":") {
		return err
	}
	return fmt.Errorf("%s: %w", pos, err)
}
func isVoidTypeExpr(typ ast.TypeExpr) bool {
	if typ == nil {
		return true
	}
	named, ok := typ.(*ast.NamedType)
	return ok && named.Name == "void"
}
