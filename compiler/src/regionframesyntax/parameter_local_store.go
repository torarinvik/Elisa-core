package regionframesyntax

import "elisacore/src/ast"

func ParamStoresBodyLocal(fn *ast.FuncDecl, name string, walk func([]ast.Stmt, func(any)), owns func(*ast.VarDeclStmt) bool, rootIdent func(ast.Expr) *ast.Ident, mentions func(any, map[string]bool) bool) bool {
	locals := map[string]bool{}
	walk(fn.Body, func(node any) {
		if v, ok := node.(*ast.VarDeclStmt); ok && v != nil && v.Name != name && owns(v) {
			locals[v.Name] = true
		}
	})
	if len(locals) == 0 {
		return false
	}
	target := map[string]bool{name: true}
	found := false
	walk(fn.Body, func(node any) {
		if found {
			return
		}
		switch n := node.(type) {
		case *ast.AssignStmt:
			if n == nil {
				return
			}
			if _, bare := n.Target.(*ast.Ident); bare {
				return
			}
			if root := rootIdent(n.Target); root != nil && root.Name == name && mentions(n.Value, locals) {
				found = true
			}
		case *ast.CallExpr:
			if n == nil {
				return
			}
			mentionsLocal := false
			for _, arg := range n.Args {
				if mentions(arg, locals) {
					mentionsLocal = true
				}
			}
			if !mentionsLocal {
				return
			}
			if field, ok := n.Func.(*ast.FieldExpr); ok && field != nil {
				if root := rootIdent(field.Object); root != nil && root.Name == name {
					found = true
					return
				}
			}
			for _, arg := range n.Args {
				if mentions(arg, target) {
					found = true
					return
				}
			}
		}
	})
	return found
}
