package interpreter

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"strings"
)

// Dictionary values. A dict lookup `d[k]` is a fallible optional reference
// (`V&?`, matching stage1): a missing key yields absent (NullValue), never a trap.
// Keys and values are kept as parallel slices in insertion order.

func DictValue(keys, values []Value) Value {
	return Value{kind: valueDict, keysVal: keys, listVal: values}
}

// isDictLiteral reports whether a brace literal builds a dict: it has keys, or the
// analyzer typed it (an empty `{}`) as a dict.
func (i *Interpreter) isDictLiteral(n *ast.ListLitExpr) bool {
	if n == nil || !n.Brace {
		return false
	}
	if n.Keys != nil {
		return true
	}
	if i == nil || i.result == nil || i.result.ExprTypes == nil {
		return false
	}
	_, ok := semantic.StripAggregateStateType(i.result.ExprTypes[n]).(*semantic.DictType)
	return ok
}

func (i *Interpreter) evalDictLiteral(frame *frame, n *ast.ListLitExpr) (Value, error) {
	keys := make([]Value, 0, len(n.Elems))
	values := make([]Value, 0, len(n.Elems))
	for idx, elem := range n.Elems {
		var key Value
		if idx < len(n.Keys) {
			k, err := i.evalExpr(frame, n.Keys[idx])
			if err != nil {
				return VoidValue(), err
			}
			key = k
		}
		value, err := i.evalExpr(frame, elem)
		if err != nil {
			return VoidValue(), err
		}
		if pos := dictKeyIndex(keys, key); pos >= 0 {
			values[pos] = value
			continue
		}
		keys = append(keys, key)
		values = append(values, value)
	}
	return DictValue(keys, values), nil
}

func dictKeyIndex(keys []Value, key Value) int {
	key = derefValue(key)
	for idx, existing := range keys {
		if valuesEqual(existing, key) {
			return idx
		}
	}
	return -1
}

// dictLookup returns the entry for key, or absent (NullValue) when it is missing.
func dictLookup(dict Value, key Value) Value {
	if pos := dictKeyIndex(dict.keysVal, key); pos >= 0 {
		return dict.listVal[pos].Clone()
	}
	return NullValue()
}

// dictStore inserts or replaces the entry for key, returning the updated dict.
func dictStore(dict Value, key Value, value Value) Value {
	updated := dict.Clone()
	if pos := dictKeyIndex(updated.keysVal, key); pos >= 0 {
		updated.listVal[pos] = value.Clone()
		return updated
	}
	updated.keysVal = append(updated.keysVal, derefValue(key).Clone())
	updated.listVal = append(updated.listVal, value.Clone())
	return updated
}

func dictString(v Value) string {
	parts := make([]string, 0, len(v.listVal))
	for idx, value := range v.listVal {
		parts = append(parts, v.keysVal[idx].String()+": "+value.String())
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// evalCondition evaluates an if/elif condition. An optional bind (`x is name`)
// is true when the value is present, and binds name for the guarded body.
func (i *Interpreter) evalCondition(frame *frame, cond ast.Expr) (bool, error) {
	if bind, ok := cond.(*ast.OptionalBindExpr); ok && bind != nil {
		value, err := i.evalExpr(frame, bind.Value)
		if err != nil {
			return false, err
		}
		if value.IsNull() {
			return false, nil
		}
		frame.locals[bind.Name] = value
		return true, nil
	}
	value, err := i.evalExpr(frame, cond)
	if err != nil {
		return false, err
	}
	return requireBool(value)
}
