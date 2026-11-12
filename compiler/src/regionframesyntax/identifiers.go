package regionframesyntax

import (
	"elisacore/src/ast"
	"reflect"
)

func SubtreeHasIdent(value reflect.Value, matches func(string) bool, fields func(reflect.Type) []int) bool {
	for value.Kind() == reflect.Interface {
		value = value.Elem()
	}
	if !value.IsValid() || !value.CanInterface() {
		return false
	}
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return false
		}
		if id, ok := value.Interface().(*ast.Ident); ok {
			return matches(id.Name)
		}
		return SubtreeHasIdent(value.Elem(), matches, fields)
	case reflect.Struct:
		for _, index := range fields(value.Type()) {
			if SubtreeHasIdent(value.Field(index), matches, fields) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if SubtreeHasIdent(value.Index(i), matches, fields) {
				return true
			}
		}
	}
	return false
}
