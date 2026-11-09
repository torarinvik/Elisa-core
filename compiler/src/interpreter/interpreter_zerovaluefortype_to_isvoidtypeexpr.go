package interpreter

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
	"strconv"
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
			return StructInstanceValue("Arena", []string{"begin", "end", "end_index", "strategy"}, map[string]Value{
				"begin":     NullValue(),
				"end":       NullValue(),
				"end_index": IntValue(0),
				"strategy":  IntValue(0),
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
