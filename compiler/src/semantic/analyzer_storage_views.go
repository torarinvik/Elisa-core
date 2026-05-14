package semantic

import (
	"fmt"

	"elisacore/src/ast"
)

func storageViewInvalidatedMessage(name string, reason string) string {
	if reason == "" {
		reason = "storage mutation"
	}
	return fmt.Sprintf("view %q cannot be used: storage dependency facts were invalidated by %s", name, reason)
}

func (a *Analyzer) reportInvalidStorageViewUse(expr ast.Expr) {
	ident, ok := stripOptimizationParens(expr).(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil || a.currentStorageViewDeps == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return
	}
	dep, ok := a.currentStorageViewDeps[sym]
	if !ok || dep.Valid {
		return
	}
	if a.storageViewStaleUses != nil {
		a.storageViewStaleUses[expr] = dep
	}
	if a.enforceUnsafePermissions {
		a.recordFunctionPermissionRefs(unsafeStaleRefRefs(expr.Pos()))
		return
	}
	a.errorf(expr.Pos(), storageViewInvalidatedMessage(ident.Name, dep.InvalidatedBy))
}

func (a *Analyzer) storageViewUseRequiresUnsafeStaleRef(expr ast.Expr) bool {
	if expr == nil || a.storageViewStaleUses == nil {
		return false
	}
	_, ok := a.storageViewStaleUses[expr]
	return ok
}

func (a *Analyzer) recordStorageViewBinding(sym *Symbol, value ast.Expr) {
	if sym == nil {
		return
	}
	dep, ok := a.storageViewDependencyForExpr(value)
	if !ok {
		if a.currentStorageViewDeps != nil {
			delete(a.currentStorageViewDeps, sym)
		}
		return
	}
	if a.currentStorageViewDeps == nil {
		a.currentStorageViewDeps = map[*Symbol]storageViewDependencyState{}
	}
	a.currentStorageViewDeps[sym] = dep
}

func (a *Analyzer) recordStorageViewAssignment(target ast.Expr, value ast.Expr) {
	ident, ok := target.(*ast.Ident)
	if !ok || a.currentScope == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return
	}
	a.recordStorageViewBinding(sym, value)
}

func (a *Analyzer) storageViewDependencyForExpr(expr ast.Expr) (storageViewDependencyState, bool) {
	if expr == nil {
		return storageViewDependencyState{}, false
	}
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.storageViewDependencyForExpr(n.Inner)
	case *ast.MoveExpr:
		return a.storageViewDependencyForExpr(n.Operand)
	case *ast.CastExpr:
		return a.storageViewDependencyForExpr(n.Operand)
	case *ast.Ident:
		if a.currentScope == nil || a.currentStorageViewDeps == nil {
			return storageViewDependencyState{}, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok {
			return storageViewDependencyState{}, false
		}
		dep, ok := a.currentStorageViewDeps[sym]
		return dep, ok
	case *ast.SliceExpr:
		if dep, ok := a.storageViewDependencyForExpr(n.Object); ok {
			return dep, true
		}
		return storageViewDependencyFromSource(n.Object)
	case *ast.CallExpr:
		return a.storageViewDependencyForCall(n)
	default:
		return storageViewDependencyState{}, false
	}
}

func (a *Analyzer) storageViewDependencyForCall(call *ast.CallExpr) (storageViewDependencyState, bool) {
	if call == nil {
		return storageViewDependencyState{}, false
	}
	name := callIdentName(call)
	switch name {
	case "darray_view", "arena_da_view":
		if len(call.Args) == 0 {
			return storageViewDependencyState{}, false
		}
		return storageViewDependencyFromSource(call.Args[0])
	case "readonly", "arena_da_view_slice", "arena_da_view_prefix", "arena_da_view_suffix":
		if len(call.Args) == 0 {
			return storageViewDependencyState{}, false
		}
		return a.storageViewDependencyForExpr(call.Args[0])
	}
	if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && field.Field == "view" && field.Object != nil {
		return storageViewDependencyFromSource(field.Object)
	}
	return storageViewDependencyState{}, false
}

func storageViewDependencyFromSource(source ast.Expr) (storageViewDependencyState, bool) {
	key := optimizationExprString(source)
	if key == "" {
		return storageViewDependencyState{}, false
	}
	return storageViewDependencyState{Source: key, Valid: true}, true
}

func (a *Analyzer) invalidateStorageViewsForSource(source ast.Expr, reason string) {
	key := optimizationExprString(source)
	if key == "" || len(a.currentStorageViewDeps) == 0 {
		return
	}
	for sym, dep := range a.currentStorageViewDeps {
		if !dep.Valid || dep.Source != key {
			continue
		}
		dep.Valid = false
		dep.InvalidatedBy = reason
		a.currentStorageViewDeps[sym] = dep
	}
}

func mergeStorageViewDependencyStates(dst map[*Symbol]storageViewDependencyState, src map[*Symbol]storageViewDependencyState) map[*Symbol]storageViewDependencyState {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[*Symbol]storageViewDependencyState, len(src))
		for sym, dep := range src {
			dst[sym] = dep
		}
		return dst
	}
	for sym, srcDep := range src {
		dstDep, ok := dst[sym]
		if !ok {
			continue
		}
		if dstDep.Source != srcDep.Source {
			delete(dst, sym)
			continue
		}
		if dstDep.Valid && !srcDep.Valid {
			dst[sym] = srcDep
		}
	}
	return dst
}

func storageViewMutationReason(source ast.Expr, operation string) string {
	key := optimizationExprString(source)
	if key == "" {
		key = "container"
	}
	return fmt.Sprintf("%s of %s", operation, key)
}
