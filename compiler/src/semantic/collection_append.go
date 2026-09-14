package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
)

func collectionAppendCall(n *ast.AugAssignStmt) *ast.CallExpr {
	return &ast.CallExpr{Position: n.Pos(), Func: &ast.FieldExpr{Position: n.Pos(), Object: n.Target, Field: "push"}, Args: []ast.Expr{n.Value}}
}

// Region signatures are inferred before expression checking. Expose the push
// spelling for typed container updates so they reuse the existing caller-region
// inference. Numeric updates remain untouched; full checking still owns validity.
func (a *Analyzer) prepareCollectionAppends(fn *ast.FuncDecl) {
	saved := a.suppressDiagnostics
	a.suppressDiagnostics = true
	defer func() { a.suppressDiagnostics = saved }()
	types := map[string]Type{}
	for _, p := range fn.Params {
		types[p.Name] = a.resolveType(p.Type)
	}
	var exprType func(ast.Expr, map[string]Type) Type
	exprType = func(e ast.Expr, env map[string]Type) Type {
		switch n := e.(type) {
		case *ast.Ident:
			return env[n.Name]
		case *ast.ParenExpr:
			return exprType(n.Inner, env)
		case *ast.FieldExpr:
			t := exprType(n.Object, env)
			if ref, ok := t.(*RefType); ok {
				t = ref.Elem
			}
			if st, ok := t.(*StructType); ok {
				return st.Fields[n.Field].Type
			}
		case *ast.IndexExpr:
			t := exprType(n.Object, env)
			if ref, ok := t.(*RefType); ok {
				t = ref.Elem
			}
			switch t := t.(type) {
			case *DArrayType:
				return t.Elem
			case *ArrayType:
				return t.Elem
			}
		}
		return nil
	}
	var walk func(reflect.Value, map[string]Type)
	walk = func(v reflect.Value, env map[string]Type) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		if list, ok := v.Interface().([]ast.Stmt); ok {
			nested := map[string]Type{}
			for k, t := range env {
				nested[k] = t
			}
			for _, stmt := range list {
				walk(reflect.ValueOf(stmt), nested)
				switch n := stmt.(type) {
				case *ast.VarDeclStmt:
					if n.Type != nil {
						nested[n.Name] = a.resolveType(n.Type)
					} else {
						nested[n.Name] = exprType(n.Value, nested)
					}
				case *ast.TupleBindStmt:
					if n.Declare {
						for _, name := range n.Names {
							delete(nested, name.Name)
						}
					}
				}
			}
			return
		}
		if n, ok := v.Interface().(*ast.AugAssignStmt); ok && n != nil {
			if n.Op == lexer.TOKEN_PLUSEQ {
				if _, _, ok := builtinDArrayPushReceiverType(exprType(n.Target, env)); ok {
					n.CollectionAppend = collectionAppendCall(n)
				}
			}
			walk(reflect.ValueOf(n.Target), env)
			walk(reflect.ValueOf(n.Value), env)
			return
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if !v.IsNil() {
				walk(v.Elem(), env)
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i), env)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), env)
			}
		}
	}
	walk(reflect.ValueOf(fn.Body), types)
}
