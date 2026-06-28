package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) analyzeCanExpr(expr *ast.CanExpr) Type {
	if expr == nil {
		return invalidType
	}
	refs := a.resolvePermissionRefs(expr.Permissions, true)
	if !expr.SuppressPermissionInference {
		a.recordFunctionPermissionRefs(refs)
	}
	return a.analyzeExpr(expr.Expr)
}

func (a *Analyzer) analyzeAllocExpr(expr *ast.AllocExpr) Type {
	return a.analyzeAllocExprWithExpected(expr, nil)
}

func allocValueExpectedType(expected Type) Type {
	refExpected, ok := expected.(*RefType)
	if !ok || refExpected == nil {
		return nil
	}
	return refExpected.Elem
}

func (a *Analyzer) analyzeAllocExprWithExpected(expr *ast.AllocExpr, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	if expr.ExplicitAutoRegion {
		a.deprecatedf(expr.Pos(), "`new[auto]` is deprecated; use `new` instead")
	}
	if expr.AutoRegion {
		return a.analyzeAutoAllocExpr(expr, expected)
	}
	if expr.Owner == nil {
		// Bare `new` defaults to region inference (`new[auto]`). The historical
		// bare-new form — a packed-enum constructor targeting an active
		// `in <store>:` scope — keeps the store path, as do explicit (non
		// recursive-plain) packed enums, whose store must be named. The flag is
		// mutated in place so downstream passes and codegen see one canonical
		// auto-alloc shape (same pattern as IndexExpr.AsSpecialize).
		if enumType, _, ok := a.packedAllocConstructorInfo(expr.Value); ok && enumType != nil && enumType.Packed {
			if _, hasStore := a.lookupPackedStore(enumType); hasStore || !enumType.RecursivePlain {
				return a.analyzeScopedPackedAllocExpr(expr)
			}
		}
		expr.AutoRegion = true
		return a.analyzeAutoAllocExpr(expr, expected)
	}
	ownerType := a.analyzeExpr(expr.Owner)
	if storeType, ok := ownerType.(*PackedEnumStoreType); ok {
		return a.analyzePackedAllocExpr(expr, storeType)
	}
	ident, ok := expr.Owner.(*ast.Ident)
	if !ok {
		a.errorf(expr.Pos(), "new[...] owner must be a region name or packed enum store, got %s", ownerType)
		a.analyzeExpr(expr.Value)
		return invalidType
	}
	_, state := a.lookupRegionState(ident.Name)
	if a.currentScope == nil {
		a.errorf(expr.Pos(), "region allocation requires function scope")
		return invalidType
	}
	// The owner is a region if it is a `region` decl (SymbolRegion) OR a binding
	// declared `owned <store>` / a moved-in owned region (registered in
	// currentRegions, see registerOwnedStoreOwner / markReceivedOwnedRegion).
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || (sym.Kind != SymbolRegion && !a.symbolIsRegionOwner(sym)) {
		a.errorf(expr.Pos(), "new[...] owner must be a region name, tree store, or packed enum store, got %s", ownerType)
		a.analyzeExpr(expr.Value)
		return invalidType
	}
	if state.Destroyed {
		a.errorf(expr.Pos(), "cannot allocate from destroyed region %q", ident.Name)
		return invalidType
	}
	state.Allocated = true
	a.currentRegions[sym] = state
	valueType := a.analyzeValueExpr(expr.Value, allocValueExpectedType(expected))
	// A region is a plain-bytes store: destroy/reset frees its contents in bulk
	// without running destructors or consuming linear handles. A value carrying
	// a linear (must-consume) handle therefore cannot live in a region — you
	// only get a region reference back, can never consume it, and bulk-free
	// would silently drop the must-consume obligation. Reject it at the
	// allocation site (a future destructor-running region capability is what
	// would lift this).
	if a.containsAffineHandleValues(valueType, map[string]bool{}) {
		a.errorf(expr.Value.Pos(), "cannot allocate a value containing linear handles into region %q: a region frees its contents in bulk (destroy/reset) without consuming them, so the linear value could never be consumed; keep it outside the region and store a borrow, or consume it explicitly", ident.Name)
	}
	return &RefType{Elem: valueType, State: RefStateNonNull, Storage: RefStorageAny, Region: ident.Name, ExplicitStorage: true}
}

// analyzeAutoAllocExpr lowers `new[auto] T(...)`: heap-allocate T into the INNERMOST active
// inferred region (the native stack arena), with no explicit region name or pool — the region is
// inferred exactly like a container's backing. The result is a region-qualified reference, so the
// existing region-provenance/escape machinery keeps it from outliving that region.
func (a *Analyzer) analyzeAutoAllocExpr(expr *ast.AllocExpr, expected Type) Type {
	region := a.activeContainerRegionName()
	if region == "" {
		a.errorf(expr.Pos(), "`new` needs a region to infer (the native stack arena), but none is in scope here; return the allocation from this function so a region is threaded in, or open an explicit `region NAME(size):` scope (or write `new[r]` to target a named region)")
		// A bare packed-enum constructor routes back into this function (docs/76 Slice 0b); re-analyzing
		// it as a value here would recurse infinitely, so stop after the diagnostic.
		if enumType, _, ok := a.packedAllocConstructorInfo(expr.Value); ok && enumType != nil && enumType.Packed {
			return invalidType
		}
		a.analyzeValueExpr(expr.Value, allocValueExpectedType(expected))
		return invalidType
	}
	// `new[auto] Expr.Variant(...)` for a PACKED enum (docs/74): the node is stored in the inferred
	// region as packed columns (no explicit Store, no `in store:`). Type-check it against the enum's
	// store layout and return the bare handle type (Expr); region provenance is attached by the
	// region-provenance pass (the AutoRegion case there records a dependency on the active region).
	if enumType, _, ok := a.packedAllocConstructorInfo(expr.Value); ok && enumType != nil && enumType.Packed {
		if enumType.StoreType == nil {
			a.errorf(expr.Pos(), "packed enum %q is missing store layout metadata", enumType.Name)
			return invalidType
		}
		localStoreType := PackedEnumStoreWithState(enumType.StoreType, a.namedTypes["Local"])
		// Register the implicit region-backed store as the active store for this enum (docs/74), so
		// subsequent `new[auto] Expr.V` and storeless `match node:` in the region resolve to it
		// without an explicit `in Store:` clause.
		a.bindActivePackedStoreType(localStoreType)
		return a.analyzePackedAllocExpr(expr, localStoreType)
	}
	valueType := a.analyzeValueExpr(expr.Value, allocValueExpectedType(expected))
	// Same constraint as an explicit region: a region bulk-frees without running destructors or
	// consuming linear handles, so a value carrying a must-consume handle cannot live in it.
	if a.containsAffineHandleValues(valueType, map[string]bool{}) {
		a.errorf(expr.Value.Pos(), "cannot allocate a value containing linear handles via new[auto]: an inferred region frees its contents in bulk without consuming them")
	}
	return &RefType{Elem: valueType, State: RefStateNonNull, Storage: RefStorageAny, Region: region, ExplicitStorage: true}
}
