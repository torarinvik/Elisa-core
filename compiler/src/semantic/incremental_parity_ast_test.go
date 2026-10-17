//go:build cgo

package semantic

import (
	"reflect"
	"testing"
)

// Include syntax outside free functions (protocol/impl declarations and lowered
// call nodes), so metadata parity cannot silently omit their published keys.
func mapIncrementalCurrentAST(t *testing.T, cold, warm reflect.Value, mapping map[PositionedFunctionNode]PositionedFunctionNode) {
	t.Helper()
	type pointerKey struct {
		typ     reflect.Type
		address uintptr
	}
	seen := map[pointerKey]pointerKey{}
	reverse := map[pointerKey]pointerKey{}
	var visit func(reflect.Value, reflect.Value)
	visit = func(left, right reflect.Value) {
		if !left.IsValid() || !right.IsValid() {
			if left.IsValid() != right.IsValid() {
				t.Fatal("AST validity differs")
			}
			return
		}
		if left.Type() != right.Type() {
			t.Fatalf("AST type differs: %s / %s", left.Type(), right.Type())
		}
		switch left.Kind() {
		case reflect.Pointer:
			if left.IsNil() || right.IsNil() {
				if left.IsNil() != right.IsNil() {
					t.Fatal("AST pointer presence differs")
				}
				return
			}
			oldKey, newKey := pointerKey{left.Type(), left.Pointer()}, pointerKey{right.Type(), right.Pointer()}
			if prior, ok := seen[oldKey]; ok {
				if prior != newKey {
					t.Fatal("AST sharing differs")
				}
				return
			}
			if prior, ok := reverse[newKey]; ok && prior != oldKey {
				t.Fatal("AST nodes unexpectedly merged")
			}
			seen[oldKey] = newKey
			reverse[newKey] = oldKey
			if left.CanInterface() {
				if node, ok := left.Interface().(PositionedFunctionNode); ok {
					current, ok := right.Interface().(PositionedFunctionNode)
					if !ok {
						t.Fatal("AST source node kind differs")
					}
					mapping[node] = current
				}
			}
			visit(left.Elem(), right.Elem())
		case reflect.Interface:
			if left.IsNil() || right.IsNil() {
				if left.IsNil() != right.IsNil() {
					t.Fatal("AST interface presence differs")
				}
				return
			}
			visit(left.Elem(), right.Elem())
		case reflect.Struct:
			for i := 0; i < left.NumField(); i++ {
				if left.Field(i).CanInterface() {
					visit(left.Field(i), right.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			if left.Len() != right.Len() {
				t.Fatal("AST sequence length differs")
			}
			for i := 0; i < left.Len(); i++ {
				visit(left.Index(i), right.Index(i))
			}
		case reflect.Map:
			if left.Len() != right.Len() {
				t.Fatal("AST map size differs")
			}
			iter := left.MapRange()
			for iter.Next() {
				key := iter.Key()
				if key.CanInterface() {
					if node, ok := key.Interface().(PositionedFunctionNode); ok {
						current, found := mapping[node]
						if !found {
							t.Fatal("AST map key lacks a current owner")
						}
						value := reflect.ValueOf(current)
						if !value.Type().AssignableTo(right.Type().Key()) {
							t.Fatal("AST map key kind differs")
						}
						key = value
					}
				}
				other := right.MapIndex(key)
				if !other.IsValid() {
					t.Fatal("AST map key differs")
				}
				visit(iter.Value(), other)
			}
		default:
			if left.CanInterface() && !reflect.DeepEqual(left.Interface(), right.Interface()) {
				t.Fatal("AST scalar differs")
			}
		}
	}
	visit(cold, warm)
}
