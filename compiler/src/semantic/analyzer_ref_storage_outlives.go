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
}

// checkStoredBorrowEscapesLocal rejects storing a freshly-taken borrow of
// function-local storage into a destination that outlives the function (a struct
// field reached through a parameter/global, a mutable global, etc.). This is the
// assignment-side analog of the return-site check: the stored reference dangles
// the instant the local frame is gone. Found by auditing the emulator, where
// path/string builders store interior references into long-lived singletons.
func (a *Analyzer) checkStoredBorrowEscapesLocal(target, value ast.Expr, valueType Type) {
	if a == nil || target == nil || !a.lvalueStorageOutlivesFunction(target) {
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
	a.checkBorrowEscapesLocal(value, valueType, "storing a reference to function-local storage into longer-lived storage; it dangles once the function returns")
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
		if region, _, ok := firstLiveRegionDependency(refState); ok && region != nil {
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
	case *RefType, *ViewType, *SViewType, *DArrayViewType:
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
		return a.borrowProvenanceStorage(n.Object)
	case *ast.FieldExpr:
		if rt, ok := a.exprTypes[n.Object].(*RefType); ok && rt != nil {
			return rt.Storage, rt.ExplicitStorage
		}
		return a.borrowProvenanceStorage(n.Object)
	}
	return RefStorageAny, false
}
