package reservebounds

import (
	"elisacore/src/ast"
	"reflect"
)

func IdentifiersResolvable(bound ast.Expr, lookup func(string) bool) bool {
	resolvable := true
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !resolvable || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if ident, ok := v.Interface().(*ast.Ident); ok {
				if !lookup(ident.Name) {
					resolvable = false
				}
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(bound))
	return resolvable
}
