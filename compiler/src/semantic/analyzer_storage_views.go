package semantic

import (
	"fmt"
	"sort"
	"strings"

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
	if storageViewScalarBindingType(sym.Type) {
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

// recordStorageViewPlaceStore: a view stored into a field or element of a local
// (`h.s <- buf.as_sview()`, `vs[0] <- view`) makes the holder's root carry the
// view's backing dependency, so a later relocation of the backing turns a read
// through the holder (or through a copy of it) into a stale-view error. The
// dependency is merged, never replaced: other fields may still hold older views.
func (a *Analyzer) recordStorageViewPlaceStore(target ast.Expr, value ast.Expr) {
	root := storageViewPlaceRoot(target)
	if root == nil {
		return
	}
	sym, ok := a.currentScope.Lookup(root.Name)
	if !ok || sym == nil || storageViewScalarBindingType(sym.Type) {
		return
	}
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
	for j, arg := range args {
		if j == index || arg == nil || a.callArgNeverReaches(call, j, index) {
			continue
		}
		a.recordStorageViewPlaceStore(target, arg)
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
	case *ast.IndexExpr:
		// An element read out of a container of views (`views[0]` over `darray[sview]`)
		// still points wherever the container's elements point.
		if resultType := a.exprTypes[n]; resultType != nil && a.typeCarriesBorrowedStorage(resultType, map[Type]bool{}) {
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
	if base := overloadedDictHelperName(name); base != "" {
		name = base
	}
	switch name {
	case "bytes_view", "bytes_view_range", "bytes_view_range_ref":
		if len(call.Args) >= 1 {
			// The view addresses the argument's buffer directly (like `as_sview`), so a callee
			// later given that storage by mutable ref -- a whole-struct `mutable P&` that may grow
			// `p.source` -- dangles it: Interior (storageViewCalleeWriteSpare still spares fields
			// the callee cannot write).
			dep, ok := storageViewDependencyFromSource(call.Args[0])
			dep.Interior = ok
			return dep, ok
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
	// An enum variant construction (`Tok.Word(v)`) carries its payloads' borrows: the
	// enum value stays dependent on whatever storage a payload view points into, so a
	// later relocation of that storage must invalidate the enum value too.
	if a.isEnumVariantConstructorCall(call) {
		var deps []storageViewDependencyState
		for _, argument := range call.Args {
			if dep, ok := a.storageViewDependencyForExpr(argument); ok {
				deps = append(deps, dep)
			}
		}
		return mergeStorageViewDependencies(deps...)
	}
	if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && field.Field == "view" && field.Object != nil {
		return storageViewDependencyFromSource(field.Object)
	}
	if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && field.Object != nil {
		switch field.Field {
		case "as_sview", "as_cstr":
			// The view addresses the receiver's buffer directly, so a callee given the
			// receiver by mutable ref (which may reassign or grow it) dangles it: Interior.
			dep, ok := storageViewDependencyFromSource(field.Object)
			dep.Interior = ok
			return dep, ok
		}
	}
	return a.storageViewDependencyForUserCall(call)
}

// storageViewDependencyForUserCall: a user function whose result may retain borrowed
// storage (`def name(self: Holder&) -> sview: return bytes_view_range(self.buf, ...)`,
// `first[T](xs: darray[T]&) -> T` over a darray of views) depends on whatever the
// arguments its return-borrow summary names depend on. Without this, a view returned
// by a helper survived a later push/replacement of the argument's storage.
func (a *Analyzer) storageViewDependencyForUserCall(call *ast.CallExpr) (storageViewDependencyState, bool) {
	if a.currentStorageViewDeps == nil && a.currentScope == nil {
		return storageViewDependencyState{}, false
	}
	resultType := a.exprTypes[call]
	// A thread handle owns the closure it runs: the spawned body reads every view the
	// closure captured, so the handle depends on them until joined.
	if resultType != nil && strings.HasPrefix(resultType.String(), "Thread[") {
		var deps []storageViewDependencyState
		for _, argument := range call.Args {
			if lambda, isLambda := stripOptimizationParens(argument).(*ast.LambdaExpr); isLambda && lambda != nil {
				if dep, ok := a.storageViewDependencyForExpr(lambda); ok {
					deps = append(deps, dep)
				}
			}
		}
		return mergeStorageViewDependencies(deps...)
	}
	if !storageViewTopLevelBorrowType(resultType) {
		// An enum result can carry a payload view into an argument (`wrap(b) -> Tok`
		// returning `Tok.Word(b.as_sview())`); trace it through the return summary.
		if _, isEnum := resultType.(*EnumType); !isEnum || !a.typeCarriesBorrowedStorage(resultType, map[Type]bool{}) {
			return storageViewDependencyState{}, false
		}
	}
	// A reference to a container HEADER (`-> mutable darray[T]&` returning `&state.items`)
	// survives the container's own growth; its relocation hazard is tracked as a container
	// alias, not as a buffer dependency.
	if ref, isRef := resultType.(*RefType); isRef && ref != nil {
		switch stripStorageViewRefs(ref.Elem).(type) {
		case *DArrayType, *DictType, *SetType:
			return storageViewDependencyState{}, false
		}
		if b, isBuiltin := stripStorageViewRefs(ref.Elem).(*BuiltinType); isBuiltin && b != nil && b.Name == "dstr" {
			return storageViewDependencyState{}, false
		}
	}
	// Trace which argument field paths the callee's returns borrow from (a syntactic
	// summary); an untraceable callee records nothing rather than every argument.
	decls, args, ok := a.storageViewOriginCallee(call)
	if !ok {
		return storageViewDependencyState{}, false
	}
	summary := a.storageViewReturnOriginsForAll(decls, 0)
	if !summary.Known {
		return storageViewDependencyState{}, false
	}
	var deps []storageViewDependencyState
	for _, origin := range summary.Origins {
		if origin.Param < 0 || origin.Param >= len(args) || args[origin.Param] == nil {
			continue
		}
		arg := stripOptimizationParens(args[origin.Param])
		passed := arg
		if addr, isAddr := arg.(*ast.AddrOfExpr); isAddr && addr != nil && addr.Operand != nil {
			arg = stripOptimizationParens(addr.Operand)
		}
		if !origin.Into {
			// The callee returns the argument itself (`idf(x: T&) -> T&: return x`), so the
			// result borrows whatever the PASSED expression borrows -- for `idf(&buf[0])` an
			// interior reference into buf's relocatable buffer, not the scalar `buf[0]`.
			if dep, ok := a.storageViewDependencyForExpr(passed); ok {
				deps = append(deps, dep)
			} else if passed != arg {
				if dep, ok := a.storageViewDependencyForExpr(arg); ok {
					deps = append(deps, dep)
				}
			}
			continue
		}
		if origin.Suffix == "" {
			if storageViewScalarBindingType(stripStorageViewRefs(a.exprTypes[arg])) {
				continue
			}
			if dep, ok := a.storageViewDependencyForExpr(arg); ok {
				deps = append(deps, dep)
			}
			// Only an argument that OWNS relocatable storage can have the result's backing
			// moved by a later mutation; arena handles hand out stable addresses.
			if !a.storageViewTypeOwnsRelocatable(stripStorageViewRefs(a.exprTypes[arg]), map[Type]bool{}) {
				continue
			}
		}
		// An `@append_only` store never moves bytes a view points into (its make_room
		// retires a full buffer instead of growing it, which the store checker enforces),
		// so the view does not depend on later borrows of the store.
		if storageViewPathCrossesAppendOnly(a.exprTypes[arg], origin.Suffix) {
			continue
		}
		key := optimizationExprString(arg)
		if key == "" {
			continue
		}
		// An "into" origin is an address into the argument's own relocatable storage, so a
		// callee later handed that storage by `mutable T&` (which may grow, clear or reassign
		// it) ends the buffer the result points into: Interior. A whole-struct borrow ends it
		// only when that callee may write the field it lies in (storageViewCalleeWriteSpare).
		deps = append(deps, storageViewDependencyState{Sources: []string{key + origin.Suffix}, Valid: true, Interior: true})
	}
	return mergeStorageViewDependencies(deps...)
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

// overloadedDictHelperName recovers the runtime dict helper behind an overload-resolved callee
// (`__ovl__arena_dict_get__mutable_dict_K_T__arena_dict_get`): the method form `m.get(k)` /
// `m.put(k, v)` is rewritten to the arena_dict_* helper and then overload-mangled, so a plain
// name switch misses it and the interior reference escapes the dict-insert invalidation.
func overloadedDictHelperName(name string) string {
	const prefix = "__ovl__"
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	visible := strings.TrimPrefix(name, prefix)
	if i := strings.Index(visible, "__"); i >= 0 {
		visible = visible[:i]
	}
	if !strings.HasPrefix(visible, "arena_dict_") {
		return ""
	}
	return visible
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

// The invalidation is a REPLACEMENT: a rehash re-slots every entry inside the bucket array even
// when a reserve_commit backing keeps its base fixed, so a stable backing cannot excuse a stale
// value reference (it would read whichever entry now occupies the old slot).
//
// invalidateStorageViewsForRelocatingDictCall invalidates interior references into a dict when a
// relocating insert (arena_dict_put/put_checked/get_or_insert, which can resize → realloc the
// bucket array) is performed on it — so a stale `arena_dict_get` reference used afterward is a
// stale-ref error rather than a silent use-after-realloc.
func (a *Analyzer) invalidateStorageViewsForRelocatingDictCall(call *ast.CallExpr) {
	if call == nil {
		return
	}
	name := callBaseName(call)
	if base := overloadedDictHelperName(name); base != "" {
		name = base
	}
	// The value-returning method form (`m <- m.put(k, v)`) stays a method call on the dict
	// receiver; it inserts into (and may rehash) the same bucket array.
	if field, ok := call.Func.(*ast.FieldExpr); ok && field != nil && name == "" {
		switch field.Field {
		case "put", "put_checked", "put_or_panic", "get_or_insert", "get_or_insert_checked", "get_or_insert_or_panic", "reserve":
			if _, isDict := stripRefForBounds(a.exprTypes[field.Object]).(*DictType); isDict {
				base := dictContainerArgBase(field.Object)
				a.invalidateStorageViewsForSourceMode(base, "dict insert (may rehash and relocate the bucket array)", true)
				a.invalidateIndexBoundsForContainer(base)
			}
		}
		return
	}
	switch name {
	case "arena_dict_put", "arena_dict_put_checked", "arena_dict_put_or_panic", "arena_dict_get_or_insert", "arena_dict_get_or_insert_checked", "arena_dict_get_or_insert_or_panic":
	default:
		return
	}
	if len(call.Args) < 2 {
		return
	}
	base := dictContainerArgBase(call.Args[1])
	a.invalidateStorageViewsForSourceMode(base, "dict insert (may rehash and relocate the bucket array)", true)
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
			merged.Replaced = merged.Replaced || dependency.Replaced
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

// checkIteratorInvalidationForEnclosingMutableRefArg rejects passing, by MUTABLE reference, a
// place that strictly ENCLOSES an actively-iterated container (`grow(&p)` or `p.step()` inside
// `for v in p.items:`). The callee reaches the iterand through the owner and may push/clear/
// replace it, relocating the buffer the loop walks; it is analyzed in its own scope, so the
// call site is the only place this is visible.
func (a *Analyzer) checkIteratorInvalidationForEnclosingMutableRefArg(arg ast.Expr) {
	if a == nil || arg == nil || len(a.currentIteratedSources) == 0 {
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
	if enclosed := iteratedSourceEnclosedBy(a.currentIteratedSources, key); enclosed != "" {
		a.errorf(arg.Pos(), "cannot pass %q by mutable reference while %q is being iterated: the callee may push/clear/replace it through %q and move its buffer out from under the loop. Iterate by index up to a saved count, or collect into a separate darray first", key, enclosed, key)
	}
}

func (a *Analyzer) invalidateStorageViewsForSource(source ast.Expr, reason string) {
	a.invalidateStorageViewsForSourceMode(source, reason, false)
}

// invalidateStorageViewsForSourceMode is invalidateStorageViewsForSource; REPLACED marks a
// whole-value store, which no stable backing can make harmless (see Replaced).
func (a *Analyzer) invalidateStorageViewsForSourceMode(source ast.Expr, reason string, replaced bool) {
	a.invalidateStorageViewsForSourceScoped(source, reason, replaced, true)
}

// iteratedSourceEnclosedBy returns the iterated place that PLACE strictly encloses (`s` encloses
// `s.items` and `s.rows[0]`), or "". The smallest key wins so the report is deterministic.
func iteratedSourceEnclosedBy[V any](iterated map[string]V, place string) string {
	best := ""
	for key := range iterated {
		if len(key) <= len(place) || key[:len(place)] != place {
			continue
		}
		if next := key[len(place)]; next != '.' && next != '[' {
			continue
		}
		if best == "" || key < best {
			best = key
		}
	}
	return best
}

// invalidateStorageViewsForSourceScoped is the chokepoint. followAliases=false limits the
// mutation to the place's OWN storage: replacing a value-typed local (`f <- other` over a
// struct built from `buf`) rewrites f's bytes and leaves `buf` -- and views into it -- intact.
func (a *Analyzer) invalidateStorageViewsForSourceScoped(source ast.Expr, reason string, replaced, followAliases bool) {
	key := optimizationExprString(source)
	if key == "" {
		return
	}
	mutatedSources := map[string]bool{key: true}
	if followAliases {
		for _, root := range a.mutationRootsForTarget(source) {
			mutatedSources[root] = true
		}
		a.expandStorageViewMutationAliases(mutatedSources)
	}
	// Iterator invalidation: relocating the buffer of a container that is being iterated
	// would leave the live iteration reading freed/stale memory. Reject it at this single
	// chokepoint that every relocating mutation (push/extend/reserve/clear/truncate) funnels
	// through — the same machinery that invalidates interior references.
	if _, iterated := a.currentIteratedSources[key]; iterated {
		a.errorf(source.Pos(), "cannot mutate %q while it is being iterated: %s would move its buffer out from under the loop. Iterate by index up to a saved count, collect into a separate darray, or back it with a stable region (reserve_commit/fixed)", key, reason)
	} else if enclosed := iteratedSourceEnclosedBy(a.currentIteratedSources, key); replaced && enclosed != "" {
		// Replacing a place that ENCLOSES the iterand (`s <- S{...}` inside `for v in
		// s.items:`) replaces the iterand's header along with it: the loop keeps walking the
		// old buffer exactly as after `s.items <- [..]`.
		a.errorf(source.Pos(), "cannot mutate %q while it is being iterated: %s would move its buffer out from under the loop. Iterate by index up to a saved count, collect into a separate darray, or back it with a stable region (reserve_commit/fixed)", enclosed, reason)
	} else {
		// Alias vector: the mutation reaches an iterated container THROUGH a borrow local
		// (`ys: mutable darray[T]& = &xs; for v in xs: ys.push(v)`). The lock keys on the
		// iterand's own spelling ("xs"), but the relocating push is emitted against "ys".
		// Resolve the mutation's laundered roots (the same alias-binding machinery the
		// mutable-alias checker uses) and reject if any of them is the locked iterand.
		for _, root := range a.mutationRootsForTarget(source) {
			if root == key || !followAliases {
				continue
			}
			if _, iterated := a.currentIteratedSources[root]; iterated {
				a.errorf(source.Pos(), "cannot mutate %q through borrow %q while %q is being iterated: %s would move its buffer out from under the loop. Iterate by index up to a saved count, collect into a separate darray, or back it with a stable region (reserve_commit/fixed)", root, key, root, reason)
				break
			}
		}
	}
	a.invalidateStorageViewDepsSparing(mutatedSources, reason, false, replaced, a.storageViewSiblingSpare(key))
}

// storageViewSiblingSpare returns a predicate naming the view sources a mutation of the field
// path KEY provably cannot reach, or nil. Sibling darray fields of one root may share a buffer
// (darray copies are shallow: `P{storage: buf, lines: buf}`), so the root-overlap rule stays the
// default. The one sound exception is a sibling whose ELEMENT type differs: a darray[u8] and a
// darray[u32] can never name the same backing, so growing `p.lines` cannot move or overwrite
// the bytes behind a view of `p.storage`. Only concrete element types qualify (builtins, sview,
// cstr, non-generic structs); anything generic keeps the conservative overlap.
func (a *Analyzer) storageViewSiblingSpare(key string) func(source, candidate string) bool {
	// An element store `p.a[i] = x` writes in place inside p.a's buffer: siblings of p.a are
	// as untouched as by a push to p.a, so the field path before the index decides.
	if cut := strings.Index(key, "["); cut >= 0 {
		key = key[:cut]
	}
	root := storageViewSourceRoot(key)
	if root == key {
		return nil
	}
	mutatedElem := a.storageViewFieldPathElem(key)
	if mutatedElem == "" {
		return nil
	}
	return func(source, candidate string) bool {
		// Alias expansion can add other paths under the same root; the mutated storage is
		// still KEY's, so the sibling argument applies to them too.
		if storageViewSourceRoot(candidate) != root {
			return false
		}
		if source == key || strings.Contains(source, "[") || storageViewSourceRoot(source) != root || source == root {
			return false
		}
		if strings.HasPrefix(source, key+".") || strings.HasPrefix(source, key+"[") || strings.HasPrefix(key, source+".") {
			return false
		}
		elem := a.storageViewFieldPathElem(source)
		return elem != "" && elem != mutatedElem
	}
}

// storageViewFieldPathElem resolves a dotted field path (`p.a.b`) from the current scope to a
// darray of a concrete element type and returns that element's key, or "".
func (a *Analyzer) storageViewFieldPathElem(path string) string {
	if a == nil || a.currentScope == nil {
		return ""
	}
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return ""
	}
	sym, ok := a.currentScope.Lookup(parts[0])
	if !ok || sym == nil {
		return ""
	}
	current := a.currentTrackedValueType(sym)
	if current == nil {
		current = sym.Type
	}
	for _, name := range parts[1:] {
		structType, ok := StripAggregateStateType(stripStorageViewRefs(current)).(*StructType)
		if !ok || structType == nil {
			return ""
		}
		field, ok := structType.Fields[name]
		if !ok {
			return ""
		}
		current = field.Type
	}
	darray, ok := StripAggregateStateType(stripStorageViewRefs(current)).(*DArrayType)
	if !ok || darray == nil {
		return ""
	}
	return storageViewConcreteElemKey(darray.Elem)
}

// storageViewConcreteElemKey names a concrete, non-generic element type, or returns "" when
// the type could stand for another (a type parameter, a generic aggregate). Equal keys are
// treated as possibly-aliasing, so a collision only costs precision.
func storageViewConcreteElemKey(t Type) string {
	switch elem := StripAggregateStateType(t).(type) {
	case *BuiltinType:
		if elem != nil {
			return "builtin " + elem.Name
		}
	case *SViewType:
		if elem != nil {
			return "sview"
		}
	case *CStrType:
		if elem != nil {
			return "cstr"
		}
	case *StructType:
		if elem != nil && len(elem.TypeParams) == 0 && len(elem.GenericParams) == 0 {
			return "struct " + elem.Namespace + "::" + elem.Name
		}
	case *EnumType:
		// Enum types carry no namespace; the type's identity distinguishes them.
		if elem != nil {
			return fmt.Sprintf("enum %p", elem)
		}
	}
	return ""
}

// storageViewPathCrossesAppendOnly: the argument type, or a struct reached along the origin's
// `.field` suffix, is an `@append_only` store (a view below it never relocates).
func storageViewPathCrossesAppendOnly(t Type, suffix string) bool {
	segments := strings.Split(strings.TrimPrefix(suffix, "."), ".")
	for i := 0; i <= len(segments); i++ {
		st, isStruct := stripStorageViewRefs(t).(*StructType)
		if !isStruct || st == nil {
			return false
		}
		if st.AppendOnly {
			return true
		}
		if i == len(segments) || segments[i] == "" {
			return false
		}
		name := segments[i]
		if cut := strings.IndexByte(name, '['); cut >= 0 {
			return false
		}
		field, ok := st.Fields[name]
		if !ok {
			return false
		}
		t = field.Type
	}
	return false
}

func stripStorageViewRefs(t Type) Type {
	for {
		ref, ok := t.(*RefType)
		if !ok || ref == nil {
			return t
		}
		t = ref.Elem
	}
}

// invalidateStorageViewsForWholeAssignment invalidates interior references into a container
// that is replaced as a whole (`xs <- [1, 2, 3]`, `s <- S{...}` over a struct holding a darray,
// or a store through a `mutable darray[T]&` parameter). The container's header now names a
// different buffer, so `&xs[0]` taken before the store reads the old one -- exactly the
// staleness a relocating push causes, and funnelled through the same chokepoint (which also
// rejects replacing a container that is being iterated). Rebinding a reference slot to another
// reference (`ys <- &zs`) leaves the old referent untouched and is not a mutation of it.
func (a *Analyzer) invalidateStorageViewsForWholeAssignment(target ast.Expr, targetType, valueType Type) {
	if a == nil || target == nil || isBorrowLikeType(valueType) {
		return
	}
	switch stripRefForBounds(targetType).(type) {
	case *DArrayType, *DictType, *SetType, *StructType:
	default:
		return
	}
	place := stripOptimizationParens(target)
	// The target's OWN facts are not invalidated: recordStorageViewAssignment rebinds them
	// from the value right after. Without this, `xs <- [h for h in xs if ...]` over an
	// element-borrowing darray (darray[sview]) made xs a view of itself, and the next
	// whole assignment poisoned xs through its own replaced binding.
	var self *Symbol
	var selfDep storageViewDependencyState
	selfHad := false
	if ident, ok := place.(*ast.Ident); ok && a.currentScope != nil {
		if sym, found := a.currentScope.Lookup(ident.Name); found {
			self = sym
			selfDep, selfHad = a.currentStorageViewDeps[sym]
		}
	}
	// Only a place reached THROUGH a reference writes someone else's container; a value-typed
	// root owns what is replaced, so aliases it was built from are not mutated.
	followAliases := true
	if root := rootIdentExpr(place); root != nil && a.currentScope != nil {
		if sym, found := a.currentScope.Lookup(root.Name); found && sym != nil {
			if _, isRef := sym.Type.(*RefType); !isRef {
				followAliases = false
			}
		}
	}
	a.invalidateStorageViewsForSourceScoped(place, storageViewMutationReason(target, "reassignment"), true, followAliases)
	if self != nil && selfHad {
		a.currentStorageViewDeps[self] = selfDep
	}
}

// invalidateStorageViewsForMutableRefArg invalidates interior references into a container passed
// to a MUTABLE reference parameter that can reach relocatable storage (a darray, or a struct that
// may hold one). The callee is analyzed in its own scope, so its push/clear through the borrow is
// invisible here; the call site is the only place a live `&xs[i]` can be seen outliving it. The
// iteration lock has its own call-site guard (checkIteratorInvalidationForMutableRefArg), so this
// touches only the storage-view facts. A stable backing (reserve_commit/fixed) still drops the
// error in the pending post-pass, exactly as for a direct push.
func (a *Analyzer) invalidateStorageViewsForMutableRefArg(arg ast.Expr, callee string) {
	a.invalidateStorageViewsForMutableRefArgSparing(arg, callee, nil)
}

// invalidateStorageViewsForMutableRefArgSparing is invalidateStorageViewsForMutableRefArg with a
// per-callee SPARE (see storageViewCalleeWriteSpare): field views the callee cannot write survive.
func (a *Analyzer) invalidateStorageViewsForMutableRefArgSparing(arg ast.Expr, callee string, spare func(source, candidate string) bool) {
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
	a.invalidateStorageViewDepsSparing(mutatedSources, fmt.Sprintf("mutable borrow of %s by %s", key, callee), true, false, spare)
}

// invalidateStorageViewDeps marks every live view depending on MUTATED_SOURCES stale. With
// INTERIOR_ONLY, only addresses into a container buffer are affected: a value merely derived
// from the container (a comprehension copy, a parse result) survives a callee's borrow, which
// cannot relocate storage the value does not point into.
func (a *Analyzer) invalidateStorageViewDeps(mutatedSources map[string]bool, reason string, interiorOnly bool) {
	a.invalidateStorageViewDepsMode(mutatedSources, reason, interiorOnly, false)
}

func (a *Analyzer) invalidateStorageViewDepsMode(mutatedSources map[string]bool, reason string, interiorOnly, replaced bool) {
	a.invalidateStorageViewDepsSparing(mutatedSources, reason, interiorOnly, replaced, nil)
}

// invalidateStorageViewDepsSparing is invalidateStorageViewDepsMode; SPARE(source, candidate)
// exempts a (view source, mutated source) pair proven disjoint (see storageViewSiblingSpare).
func (a *Analyzer) invalidateStorageViewDepsSparing(mutatedSources map[string]bool, reason string, interiorOnly, replaced bool, spare func(source, candidate string) bool) {
	candidates := sortedStorageViewSources(mutatedSources)
	a.noteStorageViewLoopMutation(candidates, reason, interiorOnly, replaced, spare)
	if len(a.currentStorageViewDeps) == 0 {
		return
	}
	for sym, dep := range a.currentStorageViewDeps {
		if !dep.Valid || (interiorOnly && !dep.Interior) {
			continue
		}
		matchedSource := storageViewDepMatchedSource(dep, candidates, spare)
		if matchedSource == "" {
			continue
		}
		dep.Valid = false
		dep.InvalidatedBy = reason
		dep.Replaced = replaced
		if matchedSource != "" {
			dep.InvalidatedBy += fmt.Sprintf(" (matched mutation source %q)", matchedSource)
		}
		a.currentStorageViewDeps[sym] = dep
	}
}

// storageViewDepMatchedSource names the first mutated candidate that reaches one of dep's
// sources (and is not spared), or "".
func storageViewDepMatchedSource(dep storageViewDependencyState, candidates []string, spare func(source, candidate string) bool) string {
	for _, source := range dep.Sources {
		for _, candidate := range candidates {
			if storageViewSourcesOverlap(source, candidate) && (spare == nil || !spare(source, candidate)) {
				return candidate
			}
		}
	}
	return ""
}

func sortedStorageViewSources(sources map[string]bool) []string {
	out := make([]string, 0, len(sources))
	for source := range sources {
		out = append(out, source)
	}
	sort.Strings(out)
	return out
}

func storageViewMutationReason(source ast.Expr, operation string) string {
	key := optimizationExprString(source)
	if key == "" {
		key = "container"
	}
	return fmt.Sprintf("%s of %s", operation, key)
}

// isEnumVariantConstructorCall reports whether CALL is `Enum.Variant(...)` for a visible
// enum type with that variant. Side-effect free (enumVariantExprType reports errors).
func (a *Analyzer) isEnumVariantConstructorCall(call *ast.CallExpr) bool {
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || field.Object == nil {
		return false
	}
	baseName, ok := qualifiedTypePathFromExpr(field.Object)
	if !ok {
		return false
	}
	base, _, ok := a.lookupVisibleType(baseName)
	if !ok {
		return false
	}
	enumType, ok := base.(*EnumType)
	if !ok || enumType == nil {
		return false
	}
	_, ok = enumType.Variant(field.Field)
	return ok
}

// storageViewTypeOwnsRelocatable: t (by value) owns storage a mutation can relocate.
func (a *Analyzer) storageViewTypeOwnsRelocatable(t Type, seen map[Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *DArrayType, *DictType, *SetType:
		return true
	case *BuiltinType:
		return isDStrType(tt)
	case *ArrayType:
		return a.storageViewTypeOwnsRelocatable(tt.Elem, seen)
	case *OptionalType:
		return a.storageViewTypeOwnsRelocatable(tt.Value, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			if a.storageViewTypeOwnsRelocatable(field.Type, seen) {
				return true
			}
		}
	case *StructType:
		for _, field := range tt.Fields {
			if a.storageViewTypeOwnsRelocatable(field.Type, seen) {
				return true
			}
		}
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := make(map[string]Type, len(base.TypeParams))
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				if a.storageViewTypeOwnsRelocatable(a.substituteType(field.Type, bindings, nil, nil, nil), seen) {
					return true
				}
			}
		}
	}
	return false
}

// storageViewTailValue is the value expression a value-position body ends with.
func storageViewTailValue(body []ast.Stmt) ast.Expr {
	if len(body) == 0 {
		return nil
	}
	if stmt, ok := body[len(body)-1].(*ast.ExprStmt); ok && stmt != nil {
		return stmt.Expr
	}
	return nil
}

// storageViewTopLevelBorrowType: the value IS a borrow (a view, a reference, or an optional
// of one), not an aggregate that merely contains some. A helper returning a freshly built
// aggregate (`semantic_loop_view(&file) -> Ast::File`) is not tracked as a view of its args.
func storageViewTopLevelBorrowType(t Type) bool {
	if opt, ok := t.(*OptionalType); ok && opt != nil {
		t = opt.Value
	}
	switch t.(type) {
	case *RefType, *ViewType, *SViewType, *CStrType:
		return true
	}
	return false
}
