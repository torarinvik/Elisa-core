package semantic

import (
	"fmt"

	"elisacore/src/ast"
)

func storageViewInvalidatedMessage(name string, reason string, sources []string, containerAliases []string) string {
	if reason == "" {
		reason = "storage mutation"
	}
	return fmt.Sprintf("view %q cannot be used: storage dependency facts were invalidated by %s (backing sources: %q; container aliases: %q)", name, reason, sources, containerAliases)
}

// pendingStorageViewError is an invalidated-view use whose final verdict waits on the region stack
// assignment (Phase C1b). Source declaration offsets are precise identities for matching every
// possible backing against reserve_commit decls in the post-pass.
type pendingStorageViewError struct {
	expr                ast.Expr
	viewName            string
	dep                 storageViewDependencyState
	sourceDeclOffsets   []int
	allSourcesHaveDecls bool
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
	if a.enforceUnsafePermissions {
		if a.storageViewStaleUses != nil {
			a.storageViewStaleUses[expr] = dep
		}
		a.recordFunctionPermissionRefs(unsafeStaleRefRefs(expr.Pos()))
		return
	}
	// Defer: if the source darray is given a reserve_commit stack (stable base) by the per-function
	// region inference, the view stays valid across growth and this error is dropped in the
	// post-pass; otherwise it is emitted there. The decl offset identifies the source precisely.
	pending := pendingStorageViewError{expr: expr, viewName: ident.Name, dep: dep, allSourcesHaveDecls: len(dep.Sources) > 0}
	for _, source := range dep.Sources {
		srcSym, ok := a.currentScope.Lookup(source)
		if !ok || srcSym == nil || srcSym.Node == nil {
			pending.allSourcesHaveDecls = false
			continue
		}
		pending.sourceDeclOffsets = append(pending.sourceDeclOffsets, srcSym.Node.Pos().Offset)
	}
	a.pendingStorageViewErrors = append(a.pendingStorageViewErrors, pending)
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
			if previous, exists := a.currentStorageViewDeps[sym]; exists && len(previous.ContainerAliases) > 0 {
				previous.Sources = nil
				previous.Valid = true
				previous.InvalidatedBy = ""
				a.currentStorageViewDeps[sym] = previous
			} else {
				delete(a.currentStorageViewDeps, sym)
			}
		}
		return
	}
	if a.currentStorageViewDeps == nil {
		a.currentStorageViewDeps = map[*Symbol]storageViewDependencyState{}
	}
	if previous, exists := a.currentStorageViewDeps[sym]; exists {
		for _, alias := range previous.ContainerAliases {
			dep.ContainerAliases = appendStorageViewSource(dep.ContainerAliases, alias)
		}
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
		if !ok || len(dep.Sources) == 0 {
			return storageViewDependencyState{}, false
		}
		return dep, true
	case *ast.SliceExpr:
		if dep, ok := a.storageViewDependencyForExpr(n.Object); ok {
			return dep, true
		}
		return storageViewDependencyFromSource(n.Object)
	case *ast.TupleExpr:
		var deps []storageViewDependencyState
		for _, element := range n.Elems {
			if dep, ok := a.storageViewDependencyForExpr(element); ok {
				deps = append(deps, dep)
			}
		}
		return mergeStorageViewDependencies(deps...)
	case *ast.ListLitExpr:
		var deps []storageViewDependencyState
		for _, element := range n.Elems {
			if dep, ok := a.storageViewDependencyForExpr(element); ok {
				deps = append(deps, dep)
			}
		}
		for _, key := range n.Keys {
			if dep, ok := a.storageViewDependencyForExpr(key); ok {
				deps = append(deps, dep)
			}
		}
		return mergeStorageViewDependencies(deps...)
	case *ast.ListComprehensionExpr:
		deps := make([]storageViewDependencyState, 0, 2)
		if listComprehensionResultMayContainBorrowedStorage(a.exprTypes[n]) {
			if dep, ok := a.storageViewDependencyForExpr(n.Source); ok {
				deps = append(deps, dep)
			} else if dep, ok := storageViewDependencyFromSource(n.Source); ok {
				deps = append(deps, dep)
			}
		}
		if dep, ok := a.storageViewDependencyForExpr(n.Value); ok {
			deps = append(deps, dep)
		}
		return mergeStorageViewDependencies(deps...)
	case *ast.TernaryExpr:
		var deps []storageViewDependencyState
		for _, arm := range []ast.Expr{n.Value, n.Alt} {
			if dep, ok := a.storageViewDependencyForExpr(arm); ok {
				deps = append(deps, dep)
			}
		}
		return mergeStorageViewDependencies(deps...)
	case *ast.StructLitExpr:
		var deps []storageViewDependencyState
		for _, argument := range n.LoweredArgs() {
			if dep, ok := a.storageViewDependencyForExpr(argument); ok {
				deps = append(deps, dep)
			}
		}
		for _, spread := range n.Spreads {
			if dep, ok := a.storageViewDependencyForExpr(spread); ok {
				deps = append(deps, dep)
			}
		}
		return mergeStorageViewDependencies(deps...)
	case *ast.RecordUpdateExpr:
		var deps []storageViewDependencyState
		if dep, ok := a.storageViewDependencyForExpr(n.Base); ok {
			deps = append(deps, dep)
		}
		for _, argument := range n.LoweredArgs() {
			if dep, ok := a.storageViewDependencyForExpr(argument); ok {
				deps = append(deps, dep)
			}
		}
		return mergeStorageViewDependencies(deps...)
	case *ast.LambdaExpr:
		return a.storageViewDependencyForLambda(n)
	case *ast.CallExpr:
		return a.storageViewDependencyForCall(n)
	default:
		return storageViewDependencyState{}, false
	}
}

// A closure keeps its captured borrowed views and relocatable container headers
// alive beyond the expression that created it. Carry those dependencies on the
// function value so a later push/rehash cannot leave a captured view dangling.
func (a *Analyzer) storageViewDependencyForLambda(lambda *ast.LambdaExpr) (storageViewDependencyState, bool) {
	if a == nil || lambda == nil || a.lambdaInfo == nil || a.currentScope == nil {
		return storageViewDependencyState{}, false
	}
	info := a.lambdaInfo[lambda]
	if info == nil {
		return storageViewDependencyState{}, false
	}
	var dependencies []storageViewDependencyState
	for _, name := range info.Captures {
		sym, ok := a.currentScope.Lookup(name)
		if !ok || sym == nil {
			continue
		}
		captured := storageViewDependencyState{Valid: true}
		if a.currentStorageViewDeps != nil {
			if dependency, exists := a.currentStorageViewDeps[sym]; exists {
				captured = dependency
				if len(captured.Sources) > 0 {
					dependencies = append(dependencies, captured)
				}
			}
		}
		for _, alias := range captured.ContainerAliases {
			dependencies = append(dependencies, storageViewDependencyState{
				Sources:       []string{alias},
				Valid:         captured.Valid,
				InvalidatedBy: captured.InvalidatedBy,
			})
		}
		captureType := a.currentTrackedValueType(sym)
		if captureType == nil {
			captureType = sym.Type
		}
		if storageViewTypeCarriesDArray(captureType, make(map[*StructType]bool)) {
			dependencies = append(dependencies, storageViewDependencyState{
				Sources:       []string{name},
				Valid:         captured.Valid,
				InvalidatedBy: captured.InvalidatedBy,
			})
		}
	}
	return mergeStorageViewDependencies(dependencies...)
}

func listComprehensionResultMayContainBorrowedStorage(typ Type) bool {
	switch result := StripAggregateStateType(typ).(type) {
	case *DArrayType:
		return typeCarriesRegionStorage(result.Elem)
	case *DictType:
		return typeCarriesRegionStorage(result.Key) || typeCarriesRegionStorage(result.Value)
	case *SetType:
		return typeCarriesRegionStorage(result.Elem)
	default:
		return false
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
			dep, ok := storageViewDependencyFromSource(p.Object)
			dep.Interior = ok
			return dep, ok
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
	case "bytes_view":
		if len(call.Args) >= 1 {
			return storageViewDependencyFromSource(call.Args[0])
		}
		return storageViewDependencyState{}, false
	case "sview", "string_view_slice", "string_view_prefix", "string_view_suffix":
		if len(call.Args) >= 1 {
			return a.storageViewDependencyForBorrowedExpr(call.Args[0])
		}
		return storageViewDependencyState{}, false
	case "arena_dict_get", "arena_dict_get_mut", "arena_dict_get_cstr_view", "arena_dict_get_cstr_view_mut":
		// arena_dict_get returns `&items[i].value` — an interior reference into the dict's bucket
		// array. A later relocating insert (arena_dict_put*/get_or_insert resize → realloc) moves
		// that array, dangling the reference. Record a dependency on the dict so the insert
		// invalidates it (the dict analogue of darray interior-ref-after-push).
		if len(call.Args) >= 1 {
			return storageViewDependencyFromSource(dictContainerArgBase(call.Args[0]))
		}
		return storageViewDependencyState{}, false
	case "arena_dict_put", "arena_dict_put_checked", "arena_dict_put_or_panic", "arena_dict_get_or_insert", "arena_dict_get_or_insert_checked", "arena_dict_get_or_insert_or_panic":
		// These also return an interior ref into the (post-resize) bucket array — depends on the
		// dict (arg 1, after the Arena) so a subsequent insert invalidates it.
		if len(call.Args) >= 2 {
			return storageViewDependencyFromSource(dictContainerArgBase(call.Args[1]))
		}
		return storageViewDependencyState{}, false
	case "slice":
		// slice(&da) borrows the darray's backing as a Slice[T]. A later relocating
		// mutation of `da` (push/resize/reserve/clear/truncate) moves that backing and
		// dangles the slice, so record a dependency on the source darray. Using the slice
		// after such a mutation is then flagged, the same as an interior-ref-after-push.
		// Gated to the borrow form `slice(&x)` (one AddrOf argument) so an unrelated
		// function named `slice` taking a value does not spuriously record a borrow.
		if len(call.Args) == 1 {
			if addr, ok := stripOptimizationParens(call.Args[0]).(*ast.AddrOfExpr); ok && addr.Operand != nil {
				return storageViewDependencyFromSource(addr.Operand)
			}
			// A source that is already a reference (`slice(xs)` where `xs: darray[T]&`, the
			// `by par` desugar's form) borrows the same backing as `slice(&xs)`.
			if ident, ok := stripOptimizationParens(call.Args[0]).(*ast.Ident); ok && ident != nil {
				return storageViewDependencyFromSource(ident)
			}
		}
		return storageViewDependencyState{}, false
	case "read_view":
		// read_view(&da) borrows the darray's backing as a read-only ReadView[T].
		// Same relocation hazard as slice(&da): a relocating mutation of `da` moves the
		// backing and dangles the view. Record a dependency so any such mutation while
		// the view is live is a stale-reference compile error.
		if len(call.Args) == 1 {
			if addr, ok := stripOptimizationParens(call.Args[0]).(*ast.AddrOfExpr); ok && addr.Operand != nil {
				return storageViewDependencyFromSource(addr.Operand)
			}
			// A source that is already a reference (`read_view(xs)` where `xs: darray[T]&`, the
			// `by par` desugar's form) borrows the same backing as `read_view(&xs)`.
			if ident, ok := stripOptimizationParens(call.Args[0]).(*ast.Ident); ok && ident != nil {
				return storageViewDependencyFromSource(ident)
			}
		}
		return storageViewDependencyState{}, false
	case "split":
		// A band borrows whatever its source slice borrows: propagate the dependency so
		// `split(whole, ...)` over a sliced darray inherits the darray dependency.
		if len(call.Args) >= 1 {
			return a.storageViewDependencyForExpr(call.Args[0])
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
		return a.storageViewDependencyForBorrowedExpr(call.Args[0])
	}
	if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && field.Field == "view" && field.Object != nil {
		return storageViewDependencyFromSource(field.Object)
	}
	if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && field.Object != nil {
		switch field.Field {
		case "as_sview", "as_cstr":
			return storageViewDependencyFromSource(field.Object)
		}
	}
	return storageViewDependencyState{}, false
}

func (a *Analyzer) storageViewDependencyForBorrowedExpr(expr ast.Expr) (storageViewDependencyState, bool) {
	if dependency, ok := a.storageViewDependencyForExpr(expr); ok && len(dependency.Sources) > 0 {
		return dependency, true
	}
	return storageViewDependencyFromSource(expr)
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
	case "arena_dict_put", "arena_dict_put_checked", "arena_dict_put_or_panic", "arena_dict_get_or_insert", "arena_dict_get_or_insert_checked", "arena_dict_get_or_insert_or_panic":
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
	return storageViewDependencyState{Sources: []string{key}, Valid: true}, true
}

func mergeStorageViewDependencies(dependencies ...storageViewDependencyState) (storageViewDependencyState, bool) {
	merged := storageViewDependencyState{Valid: true}
	seen := make(map[string]bool)
	seenAliases := make(map[string]bool)
	for _, dependency := range dependencies {
		for _, source := range dependency.Sources {
			if source != "" && !seen[source] {
				seen[source] = true
				merged.Sources = append(merged.Sources, source)
			}
		}
		for _, alias := range dependency.ContainerAliases {
			if alias != "" && !seenAliases[alias] {
				seenAliases[alias] = true
				merged.ContainerAliases = append(merged.ContainerAliases, alias)
			}
		}
		merged.Interior = merged.Interior || dependency.Interior
		if !dependency.Valid {
			merged.Valid = false
			if merged.InvalidatedBy == "" {
				merged.InvalidatedBy = dependency.InvalidatedBy
			}
		}
	}
	if len(merged.Sources) == 0 {
		return storageViewDependencyState{}, false
	}
	return merged, true
}

// checkIteratorInvalidationForMutableRefArg rejects passing an actively-iterated relocatable
// container (or a borrow alias of one) to a callee by MUTABLE reference. The function-local
// iteration lock cannot see a push/clear the callee performs through that ref (the callee is
// analyzed in its own scope), so this is the conservative-but-sound call-site guard: any
// mutable-ref pass of an iterated container is rejected, since the callee MIGHT relocate it.
// Immutable-ref args are never routed here (the caller gates on a mutable RefType param), so a
// read-only `count_it(&xs)` during iteration stays allowed.
func (a *Analyzer) checkIteratorInvalidationForMutableRefArg(arg ast.Expr) {
	if a == nil || arg == nil || len(a.currentIteratedSources) == 0 {
		return
	}
	place := arg
	if addr, ok := stripOptimizationParens(arg).(*ast.AddrOfExpr); ok {
		place = addr.Operand
	}
	key := optimizationExprString(place)
	if key != "" {
		if _, iterated := a.currentIteratedSources[key]; iterated {
			a.errorf(arg.Pos(), "cannot pass %q by mutable reference while it is being iterated: the callee may push/clear/relocate its buffer out from under the loop. Iterate by index up to a saved count, or collect into a separate darray first", key)
			return
		}
	}
	for _, root := range a.mutationRootsForTarget(place) {
		if root == key {
			continue
		}
		if _, iterated := a.currentIteratedSources[root]; iterated {
			a.errorf(arg.Pos(), "cannot pass %q (a borrow of %q) by mutable reference while %q is being iterated: the callee may relocate its buffer out from under the loop. Iterate by index up to a saved count, or collect into a separate darray first", key, root, root)
			return
		}
	}
}

func (a *Analyzer) invalidateStorageViewsForSource(source ast.Expr, reason string) {
	key := optimizationExprString(source)
	if key == "" {
		return
	}
	mutatedSources := map[string]bool{key: true}
	for _, root := range a.mutationRootsForTarget(source) {
		mutatedSources[root] = true
	}
	a.expandStorageViewMutationAliases(mutatedSources)
	// Iterator invalidation: relocating the buffer of a container that is being iterated
	// would leave the live iteration reading freed/stale memory. Reject it at this single
	// chokepoint that every relocating mutation (push/extend/reserve/clear/truncate) funnels
	// through — the same machinery that invalidates interior references.
	if _, iterated := a.currentIteratedSources[key]; iterated {
		a.errorf(source.Pos(), "cannot mutate %q while it is being iterated: %s would move its buffer out from under the loop. Iterate by index up to a saved count, collect into a separate darray, or back it with a stable region (reserve_commit/fixed)", key, reason)
	} else {
		// Alias vector: the mutation reaches an iterated container THROUGH a borrow local
		// (`ys: mutable darray[T]& = &xs; for v in xs: ys.push(v)`). The lock keys on the
		// iterand's own spelling ("xs"), but the relocating push is emitted against "ys".
		// Resolve the mutation's laundered roots (the same alias-binding machinery the
		// mutable-alias checker uses) and reject if any of them is the locked iterand.
		for _, root := range a.mutationRootsForTarget(source) {
			if root == key {
				continue
			}
			if _, iterated := a.currentIteratedSources[root]; iterated {
				a.errorf(source.Pos(), "cannot mutate %q through borrow %q while %q is being iterated: %s would move its buffer out from under the loop. Iterate by index up to a saved count, collect into a separate darray, or back it with a stable region (reserve_commit/fixed)", root, key, root, reason)
				break
			}
		}
	}
	a.invalidateStorageViewDeps(mutatedSources, reason, false)
}

// invalidateStorageViewsForMutableRefArg invalidates interior references into a container passed
// to a MUTABLE reference parameter that can reach relocatable storage (a darray, or a struct that
// may hold one). The callee is analyzed in its own scope, so its push/clear through the borrow is
// invisible here; the call site is the only place a live `&xs[i]` can be seen outliving it. The
// iteration lock has its own call-site guard (checkIteratorInvalidationForMutableRefArg), so this
// touches only the storage-view facts. A stable backing (reserve_commit/fixed) still drops the
// error in the pending post-pass, exactly as for a direct push.
func (a *Analyzer) invalidateStorageViewsForMutableRefArg(arg ast.Expr, callee string) {
	if a == nil || arg == nil || len(a.currentStorageViewDeps) == 0 {
		return
	}
	place := stripOptimizationParens(arg)
	if addr, ok := place.(*ast.AddrOfExpr); ok {
		place = addr.Operand
	}
	key := optimizationExprString(place)
	if key == "" {
		return
	}
	mutatedSources := map[string]bool{key: true}
	for _, root := range a.mutationRootsForTarget(place) {
		mutatedSources[root] = true
	}
	a.invalidateStorageViewDeps(mutatedSources, fmt.Sprintf("mutable borrow of %s by %s", key, callee), true)
}

// invalidateStorageViewDeps marks every live view depending on MUTATED_SOURCES stale. With
// INTERIOR_ONLY, only addresses into a container buffer are affected: a value merely derived
// from the container (a comprehension copy, a parse result) survives a callee's borrow, which
// cannot relocate storage the value does not point into.
func (a *Analyzer) invalidateStorageViewDeps(mutatedSources map[string]bool, reason string, interiorOnly bool) {
	if len(a.currentStorageViewDeps) == 0 {
		return
	}
	for sym, dep := range a.currentStorageViewDeps {
		if !dep.Valid || (interiorOnly && !dep.Interior) || !storageViewDependsOnAny(dep, mutatedSources) {
			continue
		}
		matchedSource := storageViewMatchedMutationSource(dep, mutatedSources)
		dep.Valid = false
		dep.InvalidatedBy = reason
		if matchedSource != "" {
			dep.InvalidatedBy += fmt.Sprintf(" (matched mutation source %q)", matchedSource)
		}
		a.currentStorageViewDeps[sym] = dep
	}
}

func storageViewMutationReason(source ast.Expr, operation string) string {
	key := optimizationExprString(source)
	if key == "" {
		key = "container"
	}
	return fmt.Sprintf("%s of %s", operation, key)
}
