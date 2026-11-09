package semantic

import (
	"elisacore/src/ast"
	"fmt"
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
	if !ok || ident == nil || a.currentScope == nil || (a.currentStorageViewDeps == nil && len(a.storageViewLoopUseFrames) == 0) {
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return
	}
	dep, ok := a.currentStorageViewDeps[sym]
	if !ok {
		// A binding that holds no view YET can still be read at the top of a loop body and
		// assigned a view further down (`if k > 0: use(v)` ... `v <- buf.as_sview()`): the
		// next iteration then reads the view the previous one left behind, which a later
		// relocation of its backing dangles. Record the use so the back-edge check sees it.
		if len(a.storageViewLoopUseFrames) > 0 && !storageViewScalarBindingType(sym.Type) {
			a.noteStorageViewLoopUse(sym, expr, ident.Name, true)
		}
		return
	}
	a.noteStorageViewLoopUse(sym, expr, ident.Name, dep.Valid)
	if dep.Valid {
		return
	}
	a.flagStaleStorageViewUse(expr, ident.Name, dep)
}

// flagStaleStorageViewUse reports a use of a view whose storage dependency is invalid: a pending
// error (or, under unsafe-permission enforcement, a required stale-ref permission).
func (a *Analyzer) flagStaleStorageViewUse(expr ast.Expr, viewName string, dep storageViewDependencyState) {
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
	pending := pendingStorageViewError{expr: expr, viewName: viewName, dep: dep, allSourcesHaveDecls: len(dep.Sources) > 0}
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
	// A scalar binding (`t: i64 = r` with `r = &xs[0]`) holds a COPY of the referent, not an
	// address into the buffer: nothing a later relocation or replacement of xs does can make
	// it stale, so it carries no storage dependency.
	// docs/128: a binding whose declared type is program-lived (`sview @source`) views bytes in
	// the program region, never a container's buffer; relocating or replacing the container it
	// was read from cannot make it stale. The type checker proves the value is program-lived.
	if storageViewScalarBindingType(sym.Type) || a.storageViewBindingIsProgramLived(sym.Type) {
		if a.currentStorageViewDeps != nil {
			delete(a.currentStorageViewDeps, sym)
		}
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

func (a *Analyzer) storageViewBindingIsProgramLived(t Type) bool {
	if t == nil || len(a.programRegions) == 0 || containsTypeParam(t) {
		return false
	}
	// A container value (a darray of program-lived views) may still share a relocatable
	// buffer with the place it was read from; only the views themselves are exempt.
	if a.storageViewTypeOwnsRelocatable(stripStorageViewRefs(t), map[Type]bool{}) {
		return false
	}
	return a.typeCanContainRegionRefs(t, map[string]bool{}) && a.typeIsProgramLivedCached(t)
}

// storageViewScalarBindingType: plain value scalars. `uintptr` is excluded -- it can hold an
// address into the buffer.
func storageViewScalarBindingType(t Type) bool {
	if b, ok := t.(*BuiltinType); ok && b != nil {
		return b.Name == "bool" || (b.Name != "uintptr" && IsNumericType(b))
	}
	_, isBitInt := t.(*BitIntType)
	return isBitInt
}

func (a *Analyzer) recordStorageViewAssignment(target ast.Expr, value ast.Expr) {
	if a.currentScope == nil {
		return
	}
	ident, ok := stripOptimizationParens(target).(*ast.Ident)
	if !ok {
		a.recordStorageViewPlaceStore(target, value)
		return
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok {
		return
	}
	a.recordStorageViewBinding(sym, value)
}

// storageViewPlaceStoreTarget validates a destination that can retain view dependencies.
func (a *Analyzer) storageViewPlaceStoreTarget(target ast.Expr) (*ast.Ident, *Symbol) {
	if a.currentScope == nil {
		return nil, nil
	}
	root := storageViewPlaceRoot(target)
	if root == nil {
		return nil, nil
	}
	sym, ok := a.currentScope.Lookup(root.Name)
	if !ok || sym == nil || storageViewScalarBindingType(sym.Type) {
		return nil, nil
	}
	// A holder of scalar-only type (`buffer: mutable darray[u8]&`) owns no pointer but its own
	// buffer: storing into it copies bytes, so it can never carry another value's view.
	if scalarOnlyType(peelRefType(sym.Type), map[Type]bool{}) {
		return nil, nil
	}
	return root, sym
}

// recordStorageViewPlaceStore: a view stored into a field or element of a local
// (`h.s <- buf.as_sview()`, `vs[0] <- view`) makes the holder's root carry the
// view's backing dependency, so a later relocation of the backing turns a read
// through the holder (or through a copy of it) into a stale-view error. The
// dependency is merged, never replaced: other fields may still hold older views.
func (a *Analyzer) recordStorageViewPlaceStore(target ast.Expr, value ast.Expr) {
	root, sym := a.storageViewPlaceStoreTarget(target)
	if sym == nil {
		return
	}
	a.recordStorageViewPlaceStoreForRoot(root, sym, value)
}

func (a *Analyzer) recordStorageViewPlaceStoreForRoot(root *ast.Ident, sym *Symbol, value ast.Expr) {
	dep, ok := a.storageViewDependencyForExpr(value)
	if !ok || len(dep.Sources) == 0 {
		return
	}
	if a.currentStorageViewDeps == nil {
		a.currentStorageViewDeps = map[*Symbol]storageViewDependencyState{}
	}
	previous, exists := a.currentStorageViewDeps[sym]
	// The store is a use of the holder in every enclosing loop, and makes it a holder there:
	// see checkStorageViewLoopHolders.
	a.noteStorageViewLoopUse(sym, root, root.Name, !exists || previous.Valid)
	a.noteStorageViewLoopHolder(sym)
	if exists {
		dep, _ = mergeStorageViewDependencies(previous, dep)
	}
	a.currentStorageViewDeps[sym] = dep
}

// recordStorageViewCalleeStores: a callee handed a container by mutable reference may store
// another argument into it (`add_row(held, v)` doing `h.push(v)`), exactly like a direct
// `held.push(v)`. The container's root then carries each such argument's backing dependency,
// so a later relocation of that backing (a later push, or the loop back edge) makes a use of
// the holder a stale-view error. The call itself is a use of the holder in every enclosing
// loop: on the next iteration it hands the callee a holder whose earlier views may dangle.
// The store-flow summary (callArgNeverReaches) spares the arguments the callee provably never
// stores into that parameter; anything it cannot prove is recorded.
func (a *Analyzer) recordStorageViewCalleeStores(call *ast.CallExpr, args []ast.Expr, index int) {
	if call == nil || index < 0 || index >= len(args) {
		return
	}
	target := stripOptimizationParens(args[index])
	if addr, ok := target.(*ast.AddrOfExpr); ok {
		target = stripOptimizationParens(addr.Operand)
	}
	// A scalar-only destination cannot retain any other argument's view. Check
	// that before computing interprocedural store summaries, once per call.
	root, sym := a.storageViewPlaceStoreTarget(target)
	if sym == nil {
		return
	}
	for j, arg := range args {
		if j == index || arg == nil || a.callArgNeverReaches(call, j, index) {
			continue
		}
		a.recordStorageViewPlaceStoreForRoot(root, sym, arg)
	}
}

func storageViewPlaceRoot(place ast.Expr) *ast.Ident {
	for place != nil {
		switch p := stripOptimizationParens(place).(type) {
		case *ast.FieldExpr:
			place = p.Object
		case *ast.IndexExpr:
			place = p.Object
		case *ast.Ident:
			return p
		default:
			return nil
		}
	}
	return nil
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
	case *ast.CanExpr:
		return a.storageViewDependencyForExpr(n.Expr)
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
		if listComprehensionResultMayContainBorrowedStorage(a.exprType(n)) {
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
	case *ast.IndexExpr:
		// An element read out of a container of views (`views[0]` over `darray[sview]`)
		// still points wherever the container's elements point.
		if resultType := a.exprType(n); resultType != nil && a.typeCarriesBorrowedStorage(resultType, map[Type]bool{}) {
			var deps []storageViewDependencyState
			if dep, ok := a.storageViewDependencyForExpr(n.Object); ok {
				deps = append(deps, dep)
			}
			if dep, ok := a.storageViewDependencyForExpr(n.Fallback); ok {
				deps = append(deps, dep)
			}
			return mergeStorageViewDependencies(deps...)
		}
		return storageViewDependencyState{}, false
	case *ast.MatchExpr:
		var deps []storageViewDependencyState
		for _, arm := range n.Arms {
			if dep, ok := a.storageViewDependencyForExpr(storageViewTailValue(arm.Body)); ok {
				deps = append(deps, dep)
			}
		}
		return mergeStorageViewDependencies(deps...)
	case *ast.ExprBlock:
		if dep, ok := a.storageViewDependencyForExpr(n.Value); ok {
			return dep, true
		}
		return a.storageViewDependencyForExpr(storageViewTailValue(n.Stmts))
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
				Replaced:      captured.Replaced,
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
				Replaced:      captured.Replaced,
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
	switch stripRefForBounds(a.exprType(obj)).(type) {
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
	dt, ok := stripRefForBounds(a.exprType(obj)).(*DArrayType)
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
