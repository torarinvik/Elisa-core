package regionframesyntax

import (
	"elisacore/src/ast"
	"reflect"
)

func ObjectMayEscape(body []ast.Stmt, name string) bool {
	okBase := map[*ast.Ident]bool{}
	allRefs := []*ast.Ident{}
	addrTaken := false
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if addrTaken || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			switch e := v.Interface().(type) {
			case *ast.Ident:
				if e.Name == name {
					allRefs = append(allRefs, e)
				}
			case *ast.FieldExpr:
				if id, ok := stripParenExpr(e.Object).(*ast.Ident); ok && id.Name == name {
					okBase[id] = true
				}
			case *ast.IndexExpr:
				if id, ok := stripParenExpr(e.Object).(*ast.Ident); ok && id.Name == name {
					okBase[id] = true
				}
			case *ast.AddrOfExpr:
				if identReducesTo(e.Operand, name) {
					addrTaken = true
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
	if addrTaken {
		return true
	}
	for _, id := range allRefs {
		if !okBase[id] {
			return true // a bare / aliasing use
		}
	}
	return false
}

// identReducesTo reports whether expr is `name` or `name[...]` (the targets of an address-of that
// would alias into the object's buffer).
func identReducesTo(expr ast.Expr, name string) bool {
	switch e := stripParenExpr(expr).(type) {
	case *ast.Ident:
		return e.Name == name
	case *ast.IndexExpr:
		id, ok := stripParenExpr(e.Object).(*ast.Ident)
		return ok && id.Name == name
	}
	return false
}

func stripParenExpr(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.Inner
	}
}
