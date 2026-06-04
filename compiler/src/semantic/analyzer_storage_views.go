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
	case *ast.AddrOfExpr:
		// A reference taken INTO a relocatable container (a darray) dangles if the
		// container is later grown/relocated (deep audit #6). Record a dependency
		// on the container so push/extend/reserve/clear/truncate invalidate the
		// interior reference, turning a later use into a stale-ref error.
		return a.storageViewDependencyForBorrowedPlace(n.Operand)
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

// storageViewDependencyForBorrowedPlace returns a dependency on the container of
// an interior reference (e.g. xs[i] in `xs[i].ref[T&]`) when that container is a
// dynamic array, whose backing buffer can move on growth. Stable storage (fixed
// arrays, struct fields, static/heap refs) does not relocate, so no dependency is
// recorded and no false positive is raised.
func (a *Analyzer) storageViewDependencyForBorrowedPlace(place ast.Expr) (storageViewDependencyState, bool) {
	switch p := stripOptimizationParens(place).(type) {
	case *ast.IndexExpr:
		if a.borrowedPlaceContainerIsRelocatable(p.Object) {
			return storageViewDependencyFromSource(p.Object)
		}
	}
	return storageViewDependencyState{}, false
}

func (a *Analyzer) borrowedPlaceContainerIsRelocatable(obj ast.Expr) bool {
	switch stripRefForBounds(a.exprTypes[obj]).(type) {
	case *DArrayType:
		// A darray backed by a reserve_commit region grows by committing pages
		// within its contiguous reservation and never relocates, so interior
		// references into it stay valid across growth (docs/68 §4). Other backings
		// (chained, heap) can relocate the buffer on growth.
		return !a.containerBackingIsStable(obj)
	}
	return false
}

// containerBackingIsStable reports whether a container's backing region never
// relocates its storage on growth — true for the single-block strategies
// reserve_commit and fixed (docs/68 §3-§4). Both occupy one contiguous block and
// grow in place via arena_realloc's tail extension (the base never moves);
// reserve_commit commits pages on demand, fixed panics on overflow. Chained/heap
// backings can relocate the buffer on growth and so are not stable.
func (a *Analyzer) containerBackingIsStable(obj ast.Expr) bool {
	dt, ok := stripRefForBounds(a.exprTypes[obj]).(*DArrayType)
	if !ok || dt.Region == "" {
		return false
	}
	_, rs := a.lookupRegionState(dt.Region)
	switch normalizeBacking(rs.Backing) {
	case "reserve_commit", "fixed":
		return true
	default:
		return false
	}
}

func (a *Analyzer) storageViewDependencyForCall(call *ast.CallExpr) (storageViewDependencyState, bool) {
	if call == nil {
		return storageViewDependencyState{}, false
	}
	name := callBaseName(call)
	switch name {
	case "arena_dict_get":
		// arena_dict_get returns `&items[i].value` — an interior reference into the dict's bucket
		// array. A later relocating insert (arena_dict_put*/get_or_insert resize → realloc) moves
		// that array, dangling the reference. Record a dependency on the dict so the insert
		// invalidates it (the dict analogue of darray interior-ref-after-push).
		if len(call.Args) >= 1 {
			return storageViewDependencyFromSource(dictContainerArgBase(call.Args[0]))
		}
		return storageViewDependencyState{}, false
	case "arena_dict_put", "arena_dict_put_checked", "arena_dict_get_or_insert":
		// These also return an interior ref into the (post-resize) bucket array — depends on the
		// dict (arg 1, after the Arena) so a subsequent insert invalidates it.
		if len(call.Args) >= 2 {
			return storageViewDependencyFromSource(dictContainerArgBase(call.Args[1]))
		}
		return storageViewDependencyState{}, false
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

// callBaseName returns a call's function name for both plain `f(...)` and specialized `f[T](...)`
// (SpecializeExpr) call forms — callIdentName only handles the plain form.
func callBaseName(call *ast.CallExpr) string {
	if call == nil {
		return ""
	}
	switch f := call.Func.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SpecializeExpr:
		if id, ok := f.Operand.(*ast.Ident); ok && id != nil {
			return id.Name
		}
	}
	return ""
}

// dictContainerArgBase peels a dict argument (`m.ref[mutable dict&]` = cast of &m, or a bare
// `m`) down to the underlying container lvalue, so the dependency source key matches between the
// borrow-producing get and the invalidating insert.
func dictContainerArgBase(expr ast.Expr) ast.Expr {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.MoveExpr:
			expr = n.Operand
		case *ast.AddrOfExpr:
			expr = n.Operand
		default:
			return expr
		}
	}
}

// invalidateStorageViewsForRelocatingDictCall invalidates interior references into a dict when a
// relocating insert (arena_dict_put/put_checked/get_or_insert, which can resize → realloc the
// bucket array) is performed on it — so a stale `arena_dict_get` reference used afterward is a
// stale-ref error rather than a silent use-after-realloc.
func (a *Analyzer) invalidateStorageViewsForRelocatingDictCall(call *ast.CallExpr) {
	if call == nil {
		return
	}
	switch callBaseName(call) {
	case "arena_dict_put", "arena_dict_put_checked", "arena_dict_get_or_insert":
	default:
		return
	}
	if len(call.Args) < 2 {
		return
	}
	base := dictContainerArgBase(call.Args[1])
	a.invalidateStorageViewsForSource(base, "dict insert (may rehash and relocate the bucket array)")
	a.invalidateIndexBoundsForContainer(base)
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
	if key == "" {
		return
	}
	// Iterator invalidation: relocating the buffer of a container that is being iterated
	// would leave the live iteration reading freed/stale memory. Reject it at this single
	// chokepoint that every relocating mutation (push/extend/reserve/clear/truncate) funnels
	// through — the same machinery that invalidates interior references.
	if _, iterated := a.currentIteratedSources[key]; iterated {
		a.errorf(source.Pos(), "cannot mutate %q while it is being iterated: %s would move its buffer out from under the loop. Iterate by index up to a saved count, collect into a separate darray, or back it with a stable region (reserve_commit/fixed)", key, reason)
	}
	if len(a.currentStorageViewDeps) == 0 {
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
