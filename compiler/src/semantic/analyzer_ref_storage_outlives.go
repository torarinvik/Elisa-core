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
