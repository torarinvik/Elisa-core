//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"testing"
)

// Include syntax outside free functions (protocol/impl declarations and lowered
// call nodes), so metadata parity cannot silently omit their published keys.
func mapIncrementalCurrentAST(t testing.TB, cold, warm reflect.Value, mapping map[PositionedFunctionNode]PositionedFunctionNode) {
	t.Helper()
	type pointerKey struct {
		typ     reflect.Type
		address uintptr
	}
	seen := map[pointerKey]pointerKey{}
	reverse := map[pointerKey]pointerKey{}
	var visit func(reflect.Value, reflect.Value, string)
	visit = func(left, right reflect.Value, path string) {
		if !left.IsValid() || !right.IsValid() {
			if left.IsValid() != right.IsValid() {
				t.Fatalf("AST validity differs at %s (%s)", path, left.Type())
			}
			return
		}
		if left.Type() != right.Type() {
			t.Fatalf("AST type differs at %s: %s / %s", path, left.Type(), right.Type())
		}
		switch left.Kind() {
		case reflect.Pointer:
			if left.IsNil() || right.IsNil() {
				if left.IsNil() != right.IsNil() {
					t.Fatalf("AST pointer presence differs at %s (%s)", path, left.Type())
				}
				return
			}
			oldKey, newKey := pointerKey{left.Type(), left.Pointer()}, pointerKey{right.Type(), right.Pointer()}
			if prior, ok := seen[oldKey]; ok {
				if prior != newKey {
					t.Fatalf("AST sharing differs at %s (%s)", path, left.Type())
				}
				return
			}
			if prior, ok := reverse[newKey]; ok && prior != oldKey {
				t.Fatalf("AST nodes unexpectedly merged at %s (%s)", path, left.Type())
			}
			seen[oldKey] = newKey
			reverse[newKey] = oldKey
			if left.CanInterface() {
				if node, ok := left.Interface().(PositionedFunctionNode); ok {
					current, ok := right.Interface().(PositionedFunctionNode)
					if !ok {
						t.Fatalf("AST source node kind differs at %s (%s)", path, left.Type())
					}
					mapping[node] = current
				}
			}
			visit(left.Elem(), right.Elem(), path+".*")
		case reflect.Interface:
			if left.IsNil() || right.IsNil() {
				if left.IsNil() != right.IsNil() {
					t.Fatalf("AST interface presence differs at %s (%s)", path, left.Type())
				}
				return
			}
			visit(left.Elem(), right.Elem(), path+".*")
		case reflect.Struct:
			for i := 0; i < left.NumField(); i++ {
				if left.Field(i).CanInterface() {
					visit(left.Field(i), right.Field(i), path+"."+left.Type().Field(i).Name)
				}
			}
		case reflect.Slice, reflect.Array:
			if left.Len() != right.Len() {
				t.Fatalf("AST sequence length differs at %s (%s)", path, left.Type())
			}
			for i := 0; i < left.Len(); i++ {
				visit(left.Index(i), right.Index(i), fmt.Sprintf("%s[%d]", path, i))
			}
		case reflect.Map:
			if left.Len() != right.Len() {
				t.Fatalf("AST map size differs at %s (%s)", path, left.Type())
			}
			iter := left.MapRange()
			for iter.Next() {
				key := iter.Key()
				if key.CanInterface() {
					if node, ok := key.Interface().(PositionedFunctionNode); ok {
						current, found := mapping[node]
						if !found {
							t.Fatalf("AST map key lacks a current owner at %s (%s)", path, left.Type())
						}
						value := reflect.ValueOf(current)
						if !value.Type().AssignableTo(right.Type().Key()) {
							t.Fatalf("AST map key kind differs at %s (%s)", path, left.Type())
						}
						key = value
					}
				}
				other := right.MapIndex(key)
				if !other.IsValid() {
					t.Fatalf("AST map key differs at %s (%s)", path, left.Type())
				}
				visit(iter.Value(), other, fmt.Sprintf("%s[%v]", path, key.Interface()))
			}
		default:
			if left.CanInterface() && !reflect.DeepEqual(left.Interface(), right.Interface()) {
				t.Fatalf("AST scalar differs at %s (%s): cold=%#v warm=%#v", path, left.Type(), left.Interface(), right.Interface())
			}
		}
	}
	visit(cold, warm, "File")
}
