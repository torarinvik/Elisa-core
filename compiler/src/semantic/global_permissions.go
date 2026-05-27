package semantic

import "elisacore/src/ast"

func isGlobalPermissionRef(ref ast.PermissionRef) bool {
	return ref.Name == "Global" && (ref.Member == "" || ref.Member == "Read" || ref.Member == "Write")
}

func permissionRefsRequiringLocalGrant(fnType *FuncType) []ast.PermissionRef {
	if fnType == nil {
		return nil
	}
	refs := functionPermissionRefs(fnType)
	if len(refs) == 0 {
		return nil
	}
	declared := grantedPermissionRefs(fnType.DeclaredPermissionRefs)
	filtered := make([]ast.PermissionRef, 0, len(refs))
	for _, ref := range refs {
		if isGlobalPermissionRef(ref) && !permissionRefGranted(ref, declared) {
			continue
		}
		filtered = append(filtered, ref)
	}
	return canonicalizePermissionRefs(filtered)
}

func isGlobalStorageSymbol(sym *Symbol) bool {
	if sym == nil {
		return false
	}
	return sym.Kind == SymbolGlobal || sym.Kind == SymbolExternVar
}

func (a *Analyzer) globalStorageSymbolForIdent(name string) (*Symbol, bool) {
	if name == "" {
		return nil, false
	}
	if a.currentScope != nil {
		if sym, ok := a.currentScope.Lookup(name); ok {
			if isGlobalStorageSymbol(sym) && !(sym.Private && !a.canAccessPrivateName(name)) {
				return sym, true
			}
			return nil, false
		}
	}
	sym, _, ok := a.lookupVisibleGlobal(name)
	if !ok || !isGlobalStorageSymbol(sym) {
		return nil, false
	}
	return sym, true
}

func (a *Analyzer) globalStorageRoot(expr ast.Expr) (*Symbol, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return a.globalStorageSymbolForIdent(n.Name)
	case *ast.FieldExpr:
		return a.globalStorageRoot(n.Object)
	case *ast.IndexExpr:
		return a.globalStorageRoot(n.Object)
	case *ast.SliceExpr:
		return a.globalStorageRoot(n.Object)
	default:
		return nil, false
	}
}

func globalStorageRootExpr(expr ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		return globalStorageRootExpr(n.Object)
	case *ast.IndexExpr:
		return globalStorageRootExpr(n.Object)
	case *ast.SliceExpr:
		return globalStorageRootExpr(n.Object)
	default:
		return expr
	}
}
