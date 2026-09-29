package semantic

import "elisacore/src/ast"

// checkRefStorageOutlives enforces the core borrow invariant from docs/26:
//
//	a borrow may not outlive the storage it points into.
//
// The dangerous, currently-unchecked move is a cast that *claims a longer-lived
// storage class than the operand actually has* — concretely, casting a borrow of
// stack/region-lived storage to a `static T&` (program-long). That is the
// `static u8&` lie in emulator.elisa: bytes built in a region are returned as
// `static u8&`, so when the region resets the reference dangles (use-after-free).
//
// validCast() checks ref nullability and named-region compatibility but NOT the
// storage class, so this slips through. We catch it here.
//
// Enforcement follows the existing unsafe-cast model (safe by default, explicit
// opt-out): a detected lifetime-widening cast requires the Unsafe.PointerCast
// permission, so it warns ("requires can[Unsafe]...") and propagates that
// permission into the function signature unless the author explicitly opts out
// with `can Unsafe.PointerCast` / a `trusted Unsafe.PointerCast:` block (or
// persists the bytes via clone[dstr] instead). The decision is computed HERE,
// during analysis, while currentScope is valid for provenance lookup, and stored
// in a.unsafeLifetimeWidenCasts; the permission validation/inference passes (which
// run with a different scope) just consult that map.
func (a *Analyzer) recordUnsafeLifetimeWiden(cast *ast.CastExpr, src, dst Type) {
	if a == nil || cast == nil {
		return
	}
	if a.castWidensRefLifetime(cast, src, dst) {
		if a.unsafeLifetimeWidenCasts == nil {
			a.unsafeLifetimeWidenCasts = make(map[*ast.CastExpr]bool)
		}
		a.unsafeLifetimeWidenCasts[cast] = true
	}
}

// castWidensRefLifetime reports whether a ref->ref cast claims a LONGER storage
// lifetime than its operand actually has — the dangling-borrow class. Two shapes:
//
//   - explicit-storage widening: a `stack`/`heap`/region ref cast to a
//     longer-lived storage class (caught by storage rank).
//   - provenance widening: a borrow whose operand provably roots at local/region
//     storage cast to `static` (catches the emulator `out[0].ref[static u8&]`
//     case, where the operand's ref storage is merely inferred `Any`).
func (a *Analyzer) castWidensRefLifetime(cast *ast.CastExpr, src, dst Type) bool {
	dstRef, ok := dst.(*RefType)
	if !ok || dstRef == nil || !dstRef.ExplicitStorage {
		return false
	}
	// Explicit-storage widening: both sides name a concrete storage class.
	if srcRef, ok := src.(*RefType); ok && srcRef != nil && srcRef.ExplicitStorage {
		if refStorageLifetimeRank(dstRef.Storage) > refStorageLifetimeRank(srcRef.Storage) {
			return true
		}
	}
	// Provenance widening: claiming program-long `static` for storage we can prove
	// is no longer-lived than its scope/region.
	if dstRef.Storage == RefStorageStatic && cast.Operand != nil {
		if prov, known := a.borrowProvenanceStorage(cast.Operand); known && prov == RefStorageStack {
			return true
		}
	}
	return false
}

// refStorageLifetimeRank orders storage classes by how long they live, so a cast
// to a higher rank is a lifetime-widening (potentially dangling) cast. Any/unknown
// is rank 0 so it never *appears* shorter than a concrete class (avoids treating
// an unknown source as a safe narrow).
func refStorageLifetimeRank(s RefStorage) int {
	switch s {
	case RefStorageStatic:
		return 3
	case RefStorageHeap:
		return 2
	case RefStorageStack:
		return 1
	default:
		return 0
	}
}

// checkReturnBorrowEscapesLocal rejects returning a borrow (ref / view / sview)
// that points into FUNCTION-LOCAL or value-parameter storage: such storage lives
// in the callee's frame and is gone the instant the function returns, so the
// returned reference dangles (deep audit #4). Region-rooted escapes are reported
// separately by the region machinery, so this only fires for plain stack/local
// provenance. A borrow that forwards a reference parameter's pointee (caller
// storage) has non-stack provenance and is correctly allowed.
//
// This is a guaranteed-dangle, not a maybe, so it is a hard error with no opt-out
// (like Rust rejecting `&local` returns) rather than an Unsafe-gated operation.
func (a *Analyzer) checkReturnBorrowEscapesLocal(value ast.Expr, valueType Type) {
	a.checkBorrowEscapesLocal(value, valueType, "returning a reference into function-local storage; it dangles once the function returns")
	if a == nil || value == nil || !a.typeCarriesBorrowedStorage(valueType, map[Type]bool{}) || isAddrOfRootedBorrow(value) {
		return
	}
	// A scalar return type copies the value out (`return out[0]` with `out: darray[i64&]`
	// derefs the element into an i64); no reference survives the return.
	if returnTypeIsScalarValue(a.currentReturn) {
		return
	}
	// Each return is checked against the per-binding store environment once the whole body has
	// been analyzed, so a clean `return H{r: z}` is not blamed for a different path's escape.
	if a.deferLocalBorrowCheck(pendingLocalBorrowStore{value: value, message: "returning an aggregate or helper result that contains a reference into function-local storage", summaryGated: true}) {
		return
	}
	flow := a.returnBorrowFlowForExpr(value, nil, map[*ast.FuncDecl]bool{}, map[*Symbol]bool{})
	if a.currentFuncDecl != nil {
		// A return expression is checked after its statements have been analyzed,
		// but this expression-only walk has no alias environment for assignments
		// to mutable locals. Summarize the enclosing function so a local aggregate
		// that was assigned a borrowed parameter is not mistaken for storage owned
		// by the local variable itself. The summary remains a may-flow: any return
		// path carrying a borrow from this frame is still rejected.
		flow = a.returnBorrowFlowForFunc(a.currentFuncDecl, map[*ast.FuncDecl]bool{})
	}
	if flow.Local {
		a.errorf(value.Pos(), "returning an aggregate or helper result that contains a reference into function-local storage; it dangles once the function returns. Return/store the value or owner by value, or clone it into a longer-lived region")
	}
}

// returnTypeIsScalarValue reports a builtin/bit-int/const-enum return type: a value that cannot
// hold a reference, so returning it never retains a borrow of the frame.
func returnTypeIsScalarValue(t Type) bool {
	switch tt := t.(type) {
	case *BuiltinType, *BitIntType, *ConstEnumType:
		return true
	case *ErrorUnionType:
		return returnTypeIsScalarValue(tt.Value)
	}
	return false
}

// checkStoredBorrowEscapesLocal rejects storing a freshly-taken borrow of
// function-local storage into a destination that outlives the function (a struct
// field reached through a parameter/global, a mutable global, etc.). This is the
// assignment-side analog of the return-site check: the stored reference dangles
// the instant the local frame is gone. Found by auditing the emulator, where
// path/string builders store interior references into long-lived singletons.
func (a *Analyzer) checkStoredBorrowEscapesLocal(target ast.Expr, targetType Type, value ast.Expr, valueType Type) {
	if a == nil || target == nil || !a.lvalueStorageOutlivesFunction(target) {
		return
	}
	// Only a borrow-like TARGET slot can CAPTURE a forwarded reference. Storing a `T&` value into a
	// non-reference target (a scalar/value field, e.g. `u32 <- u32&`) deref-copies the pointee VALUE —
	// no reference is retained, so the pointee's lifetime is irrelevant. Without this, the check
	// false-positived on the common `self.field <- ref_param` value copy (over-rejection).
	if targetType != nil && !isBorrowLikeType(targetType) {
		// An AGGREGATE slot (`out.h <- H{r: &x}`, a darray of refs) does retain the references
		// inside the stored value; walk it like a returned aggregate.
		if a.typeMayHoldFrameBorrow(targetType, map[Type]bool{}) && !a.assignTargetIsLocalRebind(target) {
			a.checkAggregateBorrowEscapesLocal(value, "storing an aggregate that contains a reference to function-local storage into longer-lived storage")
		}
		return
	}
	// Rebinding a local reference variable (e.g. `r <- &x` where r is a local
	// `T&`) does NOT escape: the local pointer slot dies with the function, so
	// pointing it at another local is fine. lvalueStorageOutlivesFunction reports
	// such a local-ref target as "outliving" because writing THROUGH it can reach
	// caller storage — but a direct rebind of the local variable is not a
	// write-through. Only writes through a reference (field/index paths) and
	// stores into globals/params can actually outlive the frame.
	if a.assignTargetIsLocalRebind(target) {
		return
	}
	// A FORWARDED reference (a plain load of a ref-typed value, e.g. a `mutable T&`
	// parameter `x`, not a freshly-taken `&place`) stored into storage that
	// outlives the function dangles iff its pointee does not outlive the function.
	// The pointee's lifetime is the value ref-type's storage class: Heap/Static
	// outlive the frame and are safe to store; Stack does not; Any is unannotated
	// and could be a caller stack local, so it is rejected conservatively (the
	// caller must promise `heap`/`static`, exactly Rust's `'static` store bound).
	// Region-rooted forwards are deferred to the region escape machinery.
	if isBorrowLikeType(valueType) && !isAddrOfRootedBorrow(value) {
		a.checkForwardedRefStoreEscapes(value, valueType)
		return
	}
	a.checkBorrowEscapesLocal(value, valueType, "storing a reference to function-local storage into longer-lived storage; it dangles once the function returns")
}

// checkRetainedBorrowEscapesLocal is the container-method analog of checkStoredBorrowEscapesLocal:
// `out.push(&x)` (or `out.push(H{r: &x})`, `out.push([&x])`) where `out` outlives the function
// retains a borrow of this frame in storage the caller still reads after the return. Unlike the
// assignment path, the pushed value is walked with the return-flow summary, so a borrow wrapped in
// a struct/list literal or a local bound to one is seen too.
func (a *Analyzer) checkRetainedBorrowEscapesLocal(receiver ast.Expr, value ast.Expr, retainedType Type, operation string) {
	if a == nil || receiver == nil || value == nil || a.staticContextDepth != 0 || !a.lvalueStorageOutlivesFunction(receiver) {
		return
	}
	// The container keeps values of its ELEMENT type: `h.r` pushed into a `darray[i64]` is a copy
	// of the pointee, not the reference.
	if retainedType == nil || !a.typeMayHoldFrameBorrow(retainedType, map[Type]bool{}) {
		return
	}
	a.checkAggregateBorrowEscapesLocal(value, operation+" stores a reference to function-local storage into longer-lived storage")
}

// checkAggregateBorrowEscapesLocal reports a value retained past the frame whose return-flow
// summary may hold a borrow of this frame's storage (`&x`, `H{r: &x}`, `[&x]`, or a local bound to
// one of those).
func (a *Analyzer) checkAggregateBorrowEscapesLocal(value ast.Expr, message string) {
	if a == nil || value == nil {
		return
	}
	// A value in a live region is also checked by the region escape machinery; this check then
	// reports only when that one did not (a region value can still hold a borrow of a stack local).
	regionDependent := false
	if refState, ok := a.regionRefStateForExpr(value); ok {
		if region, _, ok := firstLiveRegionDependency(refState); ok && region != nil && !isSynthesizedAutoRegion(region.Name) {
			regionDependent = true
		}
	}
	// What a local holds is the union of every store into it, including stores later in the body
	// (a loop's next iteration): check once the whole body has been analyzed.
	if a.deferLocalBorrowStoreCheck(value, message, regionDependent) {
		return
	}
	if regionDependent {
		return
	}
	flow := a.returnBorrowFlowForExpr(value, nil, map[*ast.FuncDecl]bool{}, map[*Symbol]bool{})
	if flow.Local {
		a.errorf(value.Pos(), "%s; it dangles once the function returns. Store the value or owner by value, or clone it into a longer-lived region", message)
	}
}

// assignTargetIsLocalRebind reports whether an assignment target is a direct
// local variable (rebinding it), as opposed to a write through a reference
// (field/index path) or a store into a global/parameter.
func (a *Analyzer) assignTargetIsLocalRebind(target ast.Expr) bool {
	ident, ok := stripOptimizationParens(target).(*ast.Ident)
	if !ok || ident == nil || a.currentScope == nil {
		return false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return false
	}
	return symbolAliasRoot(sym).Kind == SymbolLocal
}

// outlivesFrameStorage reports whether a ref storage class guarantees the
// pointee outlives the current function frame (Heap allocation or Static/global
// storage). Stack pointees die with their frame; Any is unannotated and treated
// conservatively as not-proven.
func outlivesFrameStorage(s RefStorage) bool {
	return s == RefStorageHeap || s == RefStorageStatic
}

// checkForwardedRefStoreEscapes guards the store of a forwarded reference (a
// loaded ref value, not a fresh &-borrow) into storage that outlives the
// function. Safety is decided by the pointee's storage class carried in the
// value's resolved ref type: Heap/Static outlive the frame; Stack/Any do not
// (Any is an unannotated `T&` whose pointee could be a caller stack local).
// Region-rooted forwards are handled by the region escape machinery, so they
// are exempted here to avoid double-reporting and to respect region lifetimes.
func (a *Analyzer) checkForwardedRefStoreEscapes(value ast.Expr, valueType Type) {
	if a == nil || value == nil {
		return
	}
	// Two explicit escape hatches discharge this check for a stored reference whose
	// pointee lifetime the type system cannot prove (e.g. a guest/FFI pointer cached
	// in a program-lifetime handle table):
	//   - `can Unsafe.StaleRef:` — the PREFERRED, tracked form: it surfaces
	//     Unsafe.StaleRef in the function's effect signature, so every such store is
	//     auditable through the capability system.
	//   - `trusted Unsafe.StaleRef:` — the untracked local-suppression form.
	if a.currentGrantedStaleRefDepth > 0 || a.currentTrustedStaleRefDepth > 0 {
		return
	}
	rt, ok := valueType.(*RefType)
	if !ok || rt == nil {
		return
	}
	// The pointee's lifetime is sound if EITHER the value's resolved ref type or
	// its walked provenance proves a frame-outliving (Heap/Static) storage class.
	// Both signals are individually sound when they report Heap/Static but each is
	// imprecise in a complementary way: the resolved type tracks a field/element's
	// own declared storage (e.g. a `heap T&` struct field) but is erased by a
	// `.cast[T&]` to a plain ref; the walked provenance follows the operand through
	// casts but reports a field's object storage rather than the field's own. The
	// union closes both gaps without admitting an unsound Heap/Static claim.
	if outlivesFrameStorage(rt.Storage) {
		return
	}
	// borrowProvenanceStorage only ever reports Heap (heap-typed ref) or Static
	// (global / explicit `static` cast) when it walked to genuinely frame-outliving
	// storage, so the class is trustworthy even when its `known`/explicit flag is
	// false (a bare `heap T&` carries no explicit-annotation bit).
	if prov, _ := a.borrowProvenanceStorage(value); outlivesFrameStorage(prov) {
		return
	}
	// Region-managed forwards: the region machinery already validates these
	// against the region lifetime lattice.
	if rs, ok := a.regionRefStateForExpr(value); ok {
		if region, _, ok := firstLiveRegionDependency(rs); ok && region != nil {
			return
		}
	}
	a.errorf(value.Pos(), "storing a forwarded reference into longer-lived storage; its pointee may not outlive the function. Annotate the reference as `heap`/`static` if its target outlives the frame, store the value/owner by value, or take responsibility with `can Unsafe.StaleRef:` (tracked) or `trusted Unsafe.StaleRef:`")
}

func (a *Analyzer) checkBorrowEscapesLocal(value ast.Expr, valueType Type, message string) {
	if a == nil || value == nil || !isBorrowLikeType(valueType) {
		return
	}
	// Only a FRESHLY-TAKEN borrow of a place (&x, x.ref[...], x.cast[...]) can
	// capture local-frame storage. A plain value load of a reference-typed element
	// or variable (e.g. xs[i] where xs: darray[u8&]) yields a stored reference that
	// points elsewhere, not a borrow into the local frame — never an escape here.
	if !isAddrOfRootedBorrow(value) {
		return
	}
	// Region-rooted borrows are handled (and already errored) by the region
	// escape machinery; don't double-report.
	if refState, ok := a.regionRefStateForExpr(value); ok {
		// A function-body auto region is no scope of the user's: nothing reports a borrow into it
		// leaving the function, so the storage class decides below.
		if region, _, ok := firstLiveRegionDependency(refState); ok && region != nil && !isSynthesizedAutoRegion(region.Name) {
			return
		}
	}
	if prov, known := a.borrowProvenanceStorage(value); known && prov == RefStorageStack {
		a.errorf(value.Pos(), "%s. Return/store the value or owner by value, or clone it into a longer-lived region (clone[dstr]/clone[darray[...]])", message)
	}
}

// isAddrOfRootedBorrow reports whether an expression takes the address of a place
// (directly via &, or via a .ref[...] / .cast[...] that wraps an address-of),
// i.e. it materializes a fresh borrow rather than loading a stored reference.
func isAddrOfRootedBorrow(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return isAddrOfRootedBorrow(n.Inner)
	case *ast.AddrOfExpr:
		return true
	case *ast.CastExpr:
		return isAddrOfRootedBorrow(n.Operand)
	}
	return false
}

func isBorrowLikeType(t Type) bool {
	switch t.(type) {
	case *RefType, *SViewType, *ViewType:
		return true
	}
	return false
}

// borrowProvenanceStorage reports the storage class of what an address-of /
// reference operand ultimately points INTO (not where a ref variable's slot
// lives). It returns (storage, known); known is false when provenance cannot be
// determined, in which case callers must not flag (avoid false positives).
//
// The key distinction: a binding of reference type forwards the lifetime it
// borrows (a `static u8&` param points at static storage), while a binding of
// owner/value type contributes its OWN storage (a local owner is stack/region
// lived; a global owner is program-long). Indexing/field access walks toward the
// root that establishes provenance.
func (a *Analyzer) borrowProvenanceStorage(expr ast.Expr) (RefStorage, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.borrowProvenanceStorage(n.Inner)
	case *ast.AddrOfExpr:
		return a.borrowProvenanceStorage(n.Operand)
	case *ast.CastExpr:
		if rt, ok := a.exprTypes[n].(*RefType); ok && rt != nil && rt.ExplicitStorage {
			return rt.Storage, true
		}
		return a.borrowProvenanceStorage(n.Operand)
	case *ast.Ident:
		if a.currentScope == nil {
			return RefStorageAny, false
		}
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok {
			return RefStorageAny, false
		}
		if rt, ok := sym.Type.(*RefType); ok && rt != nil {
			// A reference binding forwards the lifetime it borrows.
			return rt.Storage, rt.ExplicitStorage
		}
		switch sym.Kind {
		case SymbolGlobal:
			return RefStorageStatic, true
		case SymbolLocal, SymbolParam, SymbolRegion:
			// A local/param/region OWNER (darray/array/struct value, etc.) lives no
			// longer than its scope or owning region — never program-long.
			return RefStorageStack, true
		}
	case *ast.IndexExpr:
		// Indexing a reference borrows into what the reference points at.
		if rt, ok := a.exprTypes[n.Object].(*RefType); ok && rt != nil {
			return rt.Storage, rt.ExplicitStorage
		}
		// A container allocated in the permanent region (`in perm:`, or the body of a function
		// returning `static T&`) keeps its elements for the whole program, whoever owns the header.
		if containerRegion(a.exprTypes[n.Object]) == "perm" && a.permRegionIsBuiltin() {
			return RefStorageStatic, true
		}
		return a.borrowProvenanceStorage(n.Object)
	case *ast.FieldExpr:
		if rt, ok := a.exprTypes[n.Object].(*RefType); ok && rt != nil {
			return rt.Storage, rt.ExplicitStorage
		}
		return a.borrowProvenanceStorage(n.Object)
	}
	return RefStorageAny, false
}

// permRegionIsBuiltin reports that `perm` names the program-long region here, not a local binding.
func (a *Analyzer) permRegionIsBuiltin() bool {
	if a == nil || a.currentScope == nil {
		return true
	}
	sym, found := a.currentScope.Lookup("perm")
	return !found || sym == nil || sym.Kind == SymbolGlobal
}
