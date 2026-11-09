package interpreter

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"strings"
)

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
	// String indexing is total, like ctx_string_index/ctx_string_view_index
	// in compiled programs. Keep list indexing's bounds error below.
	if value.kind == valueString {
		if index < 0 || index >= int64(len(value.strVal)) {
			return IntValue(0), nil
		}
		return IntValue(int64(value.strVal[index])), nil
	}
	if index < 0 {
		return VoidValue(), fmt.Errorf("index %d out of range", index)
	}
	switch value.kind {
	case valueList:
		if int(index) >= len(value.listVal) {
			return VoidValue(), fmt.Errorf("index %d out of range", index)
		}
		return value.listVal[index].Clone(), nil
	default:
		return VoidValue(), fmt.Errorf("indexing requires list or string, got %s", value.String())
	}
}
func sliceValue(value Value, start, end int64) (Value, error) {
	value = derefValue(value)
	if value.kind == valueString {
		lo, hi := clampStringSliceBounds(int64(len(value.strVal)), start, end)
		return StringValue(value.strVal[lo:hi]), nil
	}
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

// clampStringSliceBounds mirrors the runtime's string_view_slice / sview
// clamping, which compiled code and the SMT view-length model both use: a
// negative start is 0, a negative end is the length, both clamp to the length,
// and an inverted range is empty. Plain string slices never trap.
func clampStringSliceBounds(length, start, end int64) (int64, int64) {
	lo := start
	if lo < 0 {
		lo = 0
	}
	hi := end
	if hi < 0 {
		hi = length
	}
	if lo > length {
		lo = length
	}
	if hi > length {
		hi = length
	}
	if hi < lo {
		hi = lo
	}
	return lo, hi
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
