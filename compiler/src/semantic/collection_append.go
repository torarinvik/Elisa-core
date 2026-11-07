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
	defer a.beginProofReportProbe()()
	saved := a.suppressDiagnostics
	a.suppressDiagnostics = true
	defer func() { a.suppressDiagnostics = saved }()
	types := map[string]Type{}
	for _, p := range fn.Params {
		types[p.Name] = a.resolveType(p.Type)
	}
	var exprType func(ast.Expr) Type
	exprType = func(e ast.Expr) Type {
		switch n := e.(type) {
		case *ast.Ident:
			return types[n.Name]
		case *ast.ParenExpr:
			return exprType(n.Inner)
		case *ast.FieldExpr:
			t := exprType(n.Object)
			if ref, ok := t.(*RefType); ok {
				t = ref.Elem
			}
			if st, ok := t.(*StructType); ok {
				return st.Fields[n.Field].Type
			}
		case *ast.IndexExpr:
			t := exprType(n.Object)
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
	// Track only the names a block changes. Unchanged outer bindings are
	// visible without copying the whole type environment for each nested block.
	type savedBinding struct {
		name string
		typ  Type
	}
	var shadowed []savedBinding
	bind := func(name string, typ Type) {
		shadowed = append(shadowed, savedBinding{name: name, typ: types[name]})
		types[name] = typ
	}
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		// Only a []ast.Stmt or an *ast.AugAssignStmt is unboxed below; test the dynamic type
		// first, since Interface() on every visited struct and scalar copies it to the heap.
		dynamic := v.Type()
		if v.Kind() == reflect.Interface {
			if v.IsNil() {
				dynamic = nil
			} else {
				dynamic = v.Elem().Type()
			}
		}
		if dynamic != collectionAppendStmtListType && dynamic != collectionAppendAugAssignType {
			switch v.Kind() {
			case reflect.Interface, reflect.Pointer:
				if !v.IsNil() {
					walk(v.Elem())
				}
			case reflect.Struct:
				for _, i := range syntaxWalkFields(v.Type()) {
					walk(v.Field(i))
				}
			case reflect.Slice, reflect.Array:
				for i := 0; i < v.Len(); i++ {
					walk(v.Index(i))
				}
			}
			return
		}
		if list, ok := v.Interface().([]ast.Stmt); ok {
			start := len(shadowed)
			for _, stmt := range list {
				walk(reflect.ValueOf(stmt))
				switch n := stmt.(type) {
				case *ast.VarDeclStmt:
					if n.Type != nil {
						bind(n.Name, a.resolveType(n.Type))
					} else {
						bind(n.Name, exprType(n.Value))
					}
				case *ast.TupleBindStmt:
					if n.Declare {
						for _, name := range n.Names {
							bind(name.Name, nil)
						}
					}
				}
			}
			for i := len(shadowed) - 1; i >= start; i-- {
				prior := shadowed[i]
				if prior.typ == nil {
					delete(types, prior.name)
				} else {
					types[prior.name] = prior.typ
				}
			}
			shadowed = shadowed[:start]
			return
		}
		if n, ok := v.Interface().(*ast.AugAssignStmt); ok && n != nil {
			if n.Op == lexer.TOKEN_PLUSEQ {
				if _, _, ok := builtinDArrayPushReceiverType(exprType(n.Target)); ok {
					n.CollectionAppend = collectionAppendCall(n)
				}
			}
			walk(reflect.ValueOf(n.Target))
			walk(reflect.ValueOf(n.Value))
			return
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Struct:
			for _, i := range syntaxWalkFields(v.Type()) {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(fn.Body))
}

var (
	collectionAppendStmtListType  = reflect.TypeOf([]ast.Stmt(nil))
	collectionAppendAugAssignType = reflect.TypeOf((*ast.AugAssignStmt)(nil))
)
