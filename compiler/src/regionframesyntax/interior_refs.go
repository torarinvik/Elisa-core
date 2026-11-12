package regionframesyntax

import (
	"elisacore/src/ast"
	"reflect"
)

func InteriorRefNames(body []ast.Stmt, tracked map[string]bool) map[string]bool {
	names := map[string]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if addr, ok := v.Interface().(*ast.AddrOfExpr); ok {
				if idx, ok := stripParenExpr(addr.Operand).(*ast.IndexExpr); ok {
					if id, ok := stripParenExpr(idx.Object).(*ast.Ident); ok && tracked[id.Name] {
						names[id.Name] = true
					}
				}
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
	walk(reflect.ValueOf(body))
	return names
}
