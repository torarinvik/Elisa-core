package semantic

import "elisacore/src/ast"

func isGlobalPermissionRef(ref ast.PermissionRef) bool {
	return ref.Name == "Global" && (ref.Member == "" || ref.Member == "Read" || ref.Member == "Write")
}

func (a *Analyzer) permissionRefsRequiringLocalGrant(fnType *FuncType) []ast.PermissionRef {
	if fnType == nil {
		return nil
	}
	refs := functionPermissionRefs(fnType)
	if len(refs) == 0 {
		return nil
	}
	declared := a.grantedPermissionRefs(fnType.DeclaredPermissionRefs)
	filtered := make([]ast.PermissionRef, 0, len(refs))
	for _, ref := range refs {
		// A Global ref that the function DECLARED is always enforced -- that has been
		// true since the family was added, and is what lets a caller be told it needs
		// `can[Global.Write]` to reach a declared writer. An INFERRED one is dropped
		// unless `-Wglobals` is on, because demanding it everywhere would rewrite the
		// `can` row of every function in every existing program that touches a global.
		if isGlobalPermissionRef(ref) && !permissionRefGranted(ref, declared) && !a.enforceGlobalPermissions {
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
	case *ast.ParenExpr:
		return a.globalStorageRoot(n.Inner)
	default:
		return nil, false
	}
}

// growthReceiverIsProgramLifetime reports whether an in-place container growth
// op (push/extend/reserve/resize on a darray/store/dict) targets storage rooted
// at a GLOBAL. Such a container is program-lifetime, so its backing allocation
// belongs in the permanent region — the in-place analogue of the global-store
// inference (a global `g <- Build()` binds to perm). When true the growth needs
// no ambient `in <arena>:` scope; the backend routes it to the perm arena.
func (a *Analyzer) growthReceiverIsProgramLifetime(opExpr, receiver ast.Expr) bool {
	if a == nil || receiver == nil {
		return false
	}
	if _, ok := a.globalStorageRoot(receiver); ok {
		// Record so the backend routes this growth's allocation to the perm arena
		// (the semantic side already suppressed the missing-region error).
		if opExpr != nil && a.permGrowthOps != nil {
			a.permGrowthOps[opExpr] = true
		}
		return true
	}
	return false
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
