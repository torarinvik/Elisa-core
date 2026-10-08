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
		// Mutable-global effects and explicitly declared Global rows are mandatory.
		// Keep legacy inferred immutable-global/extern effects behind -Wglobals.
		if isGlobalPermissionRef(ref) && !permissionRefGranted(ref, declared) && !a.enforceGlobalPermissions && !permissionRefGranted(ref, a.grantedPermissionRefs(a.mandatoryGlobalPermissionRefs(fnType))) {
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
			if isGlobalStorageSymbol(sym) && a.globalNameIsVisible(sym, name) {
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
		if sym, known := a.resolvedGlobalStorage[n]; known {
			return sym, isGlobalStorageSymbol(sym)
		}
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

// Save lexical resolution while parameters and block locals are still in scope.
// A nil entry records a local, preventing later file-scope validation from mistaking
// shadowed names for globals.
func (a *Analyzer) recordResolvedGlobalStorage(expr *ast.Ident, sym *Symbol) {
	if a.suppressDiagnostics {
		return
	}
	if !isGlobalStorageSymbol(sym) {
		// Ordinary local names need no cached entry; only shadowing a visible
		// global can be confused by the later file-scope traversal.
		global, _, ok := a.lookupVisibleGlobal(expr.Name)
		if !ok || !isGlobalStorageSymbol(global) {
			return
		}
		sym = nil
	}
	if a.resolvedGlobalStorage == nil {
		a.resolvedGlobalStorage = make(map[*ast.Ident]*Symbol)
	}
	a.resolvedGlobalStorage[expr] = sym
}

func (a *Analyzer) mutableGlobalStorageRoot(expr ast.Expr) (*Symbol, bool) {
	sym, ok := a.globalStorageRoot(expr)
	return sym, ok && sym.Kind == SymbolGlobal && sym.Mutable
}

// Calls through mutable references and mutating collection receivers can write
// global storage even when the syntax contains no assignment statement.
func (a *Analyzer) mutableGlobalCallWriteRefs(call *ast.CallExpr) []ast.PermissionRef {
	var refs []ast.PermissionRef
	add := func(expr ast.Expr) {
		for {
			switch n := expr.(type) {
			case *ast.AddrOfExpr:
				expr = n.Operand
			case *ast.CastExpr:
				expr = n.Operand
			case *ast.ParenExpr:
				expr = n.Inner
			default:
				if _, ok := a.mutableGlobalStorageRoot(expr); ok {
					refs = append(refs, globalWriteRefs(expr.Pos())...)
				}
				return
			}
		}
	}
	if receiver, ok := a.mutatingBuiltinMethodReceiver(call); ok {
		add(receiver)
	}
	if ft, ok := a.exprTypes[call.Func].(*FuncType); ok {
		args := call.Args
		if aligned, ok := a.callAlignedAliasArgs[call]; ok {
			args = aligned
		}
		for i, arg := range args {
			if i >= len(ft.Params) {
				break
			}
			if ref, ok := ft.Params[i].(*RefType); ok && ref.Mutable {
				add(arg)
			}
		}
	}
	return canonicalizePermissionRefs(refs)
}

func globalReferenceStorageExpr(expr ast.Expr) ast.Expr {
	for {
		switch n := expr.(type) {
		case *ast.AddrOfExpr:
			expr = n.Operand
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.ParenExpr:
			expr = n.Inner
		default:
			return expr
		}
	}
}

func (a *Analyzer) mutableGlobalReturnedRefRefs(value ast.Expr, returnType Type) []ast.PermissionRef {
	if mutableGlobalReferenceType(returnType) {
		if root := globalReferenceStorageExpr(value); root != nil {
			if _, global := a.mutableGlobalStorageRoot(root); global {
				return globalWriteRefs(root.Pos())
			}
		}
	}
	return nil
}

func (a *Analyzer) mandatoryGlobalPermissionRefs(fn *FuncType) []ast.PermissionRef {
	if fn == nil {
		return nil
	}
	refs := append([]ast.PermissionRef(nil), fn.MutableGlobalPermissionRefs...)
	for _, name := range fn.MutableGlobalSourceNames {
		if source := a.functionTypes[name]; source != nil {
			refs = append(refs, source.MutableGlobalPermissionRefs...)
		}
	}
	// Generic/function-value copies may predate the effect fixpoint. Resolve the
	// named declaration's final mutable row instead of trusting that stale copy.
	if sym, _, ok := a.lookupVisibleGlobal(fn.Name); ok {
		if declared, ok := sym.Type.(*FuncType); ok {
			refs = append(refs, declared.MutableGlobalPermissionRefs...)
		}
	}
	for _, ref := range fn.DeclaredPermissionRefs {
		if isGlobalPermissionRef(ref) {
			refs = append(refs, ref)
		}
	}
	return canonicalizePermissionRefs(refs)
}

func mutableGlobalReferenceType(t Type) bool {
	for {
		switch n := t.(type) {
		case *OptionalType:
			t = n.Value
		case *RefType:
			return n.Mutable
		default:
			return false
		}
	}
}

// Handing a mutable-global callback to a higher-order callee transfers the
// ability to perform its accesses, just as handing out a writable reference does.
func (a *Analyzer) mutableGlobalCallbackRefs(call *ast.CallExpr) []ast.PermissionRef {
	var refs []ast.PermissionRef
	for _, arg := range call.LoweredArgs() {
		if callback, ok := a.exprTypes[arg].(*FuncType); ok {
			refs = append(refs, a.mandatoryGlobalPermissionRefs(callback)...)
		}
	}
	return canonicalizePermissionRefs(refs)
}
