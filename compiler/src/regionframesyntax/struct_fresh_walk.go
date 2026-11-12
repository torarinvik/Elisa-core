package regionframesyntax

import (
	"elisacore/src/ast"
	"reflect"
)

func StructFieldsRemainFresh(body []ast.Stmt, constructed map[string]bool, unwrap func(ast.Expr) ast.Expr, freshLiteral func(*ast.StructLitExpr) bool, freshAssignment func(ast.Expr) bool) bool {
	ok := true
	var visit func(v reflect.Value)
	visit = func(v reflect.Value) {
		if !ok || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			switch n := v.Interface().(type) {
			case *ast.VarDeclStmt:
				if constructed[n.Name] {
					if lit, isLit := unwrap(n.Value).(*ast.StructLitExpr); isLit && !freshLiteral(lit) {
						ok = false
						return
					}
				}
			case *ast.AssignStmt:
				if !freshAssignment(n.Target) {
					ok = false
					return
				}
			}
			visit(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				visit(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i))
			}
		}
	}
	visit(reflect.ValueOf(body))
	return ok
}
