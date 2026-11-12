package regionframesyntax

import (
	"elisacore/src/ast"
	"elisacore/src/astprunedscan"
)

func ParamFieldAssignedFreshContainer(stmts []ast.Stmt, name string, funcByName map[string]*ast.FuncDecl, isAuto func(*ast.RegionStmt) bool, fresh func(ast.Expr) bool, strip func(ast.Expr) ast.Expr, rootIdent func(ast.Expr) *ast.Ident, scalar func(ast.TypeExpr) bool) bool {
	freshLocals := map[string]bool{}
	var assigns []*ast.AssignStmt
	astprunedscan.WalkPruned(stmts, func(node any) bool {
		switch n := node.(type) {
		case *ast.InStoreStmt:
			return false
		case *ast.RegionStmt:
			if !isAuto(n) {
				return false
			}
		case *ast.VarDeclStmt:
			if n == nil {
				return true
			}
			if _, isStr := n.Value.(*ast.StringLit); n.Name != "" && !isStr && fresh(n.Value) {
				freshLocals[n.Name] = true
			}
			// A struct-literal local (`local: Inner = Inner{xs: []}`) owns fresh container
			// fields that the body then grows; storing it (or one of its fields) into the
			// param must build those fields in the caller's region too.
			if _, isLit := strip(n.Value).(*ast.StructLitExpr); n.Name != "" && isLit {
				freshLocals[n.Name] = true
			}
		case *ast.AssignStmt:
			if n != nil && !n.Optional {
				assigns = append(assigns, n)
			}
		}
		return true
	})
	for _, asn := range assigns {
		field, ok := asn.Target.(*ast.FieldExpr)
		if !ok || field == nil {
			continue
		}
		if root := rootIdent(field); root == nil || root.Name != name {
			continue
		}
		value := asn.Value
		for {
			paren, ok := value.(*ast.ParenExpr)
			if !ok {
				break
			}
			value = paren.Inner
		}
		if id, ok := value.(*ast.Ident); ok && id != nil && freshLocals[id.Name] {
			return true
		}
		// `o.inner.xs <- local.xs`: a field path of a fresh local carries its fresh backing.
		if field, ok := value.(*ast.FieldExpr); ok && field != nil {
			if root := rootIdent(field); root != nil && freshLocals[root.Name] {
				return true
			}
		}
		if _, isStruct := value.(*ast.StructLitExpr); isStruct {
			return true
		}
		// `flow.normal <- snapshot(names)`: a region-polymorphic factory builds its result in
		// the ambient region, which must then be the param's (caller's) region, not this
		// body's auto region freed at return. A non-polymorphic callee is unaffected. Only a
		// free-function FACTORY qualifies: a call that reads the param (`p.f <- p.f.push(v)`,
		// `p.f <- grow(p.f)`) rebinds existing storage and builds nothing fresh.
		// A callee returning a scalar (`p.head <- index_of(p, 1)`) builds no container either.
		if call, isCall := value.(*ast.CallExpr); isCall && call != nil {
			if callee, isFree := call.Func.(*ast.Ident); isFree {
				if fd := funcByName[callee.Name]; fd != nil && scalar(fd.ReturnType) {
					continue
				}
				return true
			}
		}
		if _, isStr := value.(*ast.StringLit); !isStr && fresh(value) {
			return true
		}
	}
	return false
}
