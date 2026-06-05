package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
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
	if expr.AutoRegion {
		return a.analyzeAutoAllocExpr(expr, expected)
	}
	if expr.Owner == nil {
		return a.analyzeScopedPackedAllocExpr(expr)
	}
	if updateExpr, ok := expr.Value.(*ast.RecordUpdateExpr); ok && updateExpr != nil {
		baseType := a.analyzeExpr(updateExpr.Base)
		if _, exact := TreeExactTag(baseType); exact {
			owner, ownerType, ownerOK := a.classifyTreeAllocOwnerExpr(expr.Owner)
			family, _ := TreeFamilyForMemberType(baseType)
			if !ownerOK {
				if ownerType != nil {
					a.errorf(allocOwnerPos(expr), "tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference, got %s", ownerType)
				} else {
					a.errorf(allocOwnerPos(expr), "tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference, got %s", "<invalid>")
				}
				return a.analyzeRecordUpdateExprWithTreeOwnerRequirement(updateExpr, false)
			}
			if owner.Kind == treeAllocOwnerStore && owner.StoreFamily != nil && family != owner.StoreFamily {
				a.errorf(allocOwnerPos(expr), "tree update of %q requires store %q, got %q", baseType, family.StoreType, ownerType)
			}
			return a.analyzeRecordUpdateExprWithTreeOwnerRequirement(updateExpr, false)
		}
	}
	if treeType, variant, callExpr, ok := a.treeAllocConstructorInfo(expr.Value); ok {
		owner, ownerType, ownerOK := a.classifyTreeAllocOwnerExpr(expr.Owner)
		if !ownerOK {
			if ownerType != nil {
				a.errorf(allocOwnerPos(expr), "tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference, got %s", ownerType)
			} else {
				a.errorf(allocOwnerPos(expr), "tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference, got %s", "<invalid>")
			}
			return a.analyzeTreeAllocExpr(expr, treeType, variant, callExpr)
		}
		if owner.Kind == treeAllocOwnerStore && owner.StoreFamily != nil && treeType.Family != owner.StoreFamily {
			a.errorf(allocOwnerPos(expr), "tree constructor %q requires store %q, got %q", treeType.Name+"."+variant.Name, treeType.Family.StoreType, ownerType)
		}
		return a.analyzeTreeAllocExpr(expr, treeType, variant, callExpr)
	}
	if memberType, callExpr, ok := a.treeExactAllocConstructorInfo(expr.Value); ok {
		owner, ownerType, ownerOK := a.classifyTreeAllocOwnerExpr(expr.Owner)
		family, _ := TreeFamilyForMemberType(memberType)
		if !ownerOK {
			if ownerType != nil {
				a.errorf(allocOwnerPos(expr), "tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference, got %s", ownerType)
			} else {
				a.errorf(allocOwnerPos(expr), "tree allocation owner must be perm, a tree store, an Arena value, or an Arena reference, got %s", "<invalid>")
			}
			return a.analyzeTreeExactAllocExpr(expr, memberType, callExpr)
		}
		if owner.Kind == treeAllocOwnerStore && owner.StoreFamily != nil && family != owner.StoreFamily {
			a.errorf(allocOwnerPos(expr), "tree constructor %q requires store %q, got %q", memberType, family.StoreType, ownerType)
		}
		return a.analyzeTreeExactAllocExpr(expr, memberType, callExpr)
	}
	if isTreeAllocPermExpr(expr.Owner) {
		valueType := a.analyzeExpr(expr.Value)
		a.errorf(expr.Value.Pos(), "new[perm] expects a tree constructor, got %s", valueType)
		return invalidType
	}
	ownerType := a.analyzeExpr(expr.Owner)
	if storeType, ok := ownerType.(*PackedEnumStoreType); ok {
		return a.analyzePackedAllocExpr(expr, storeType)
	}
	if _, ok := ownerType.(*TreeStoreType); ok {
		valueType := a.analyzeExpr(expr.Value)
		a.errorf(expr.Value.Pos(), "new[%s] expects a tree constructor, got %s", ownerType, valueType)
		return invalidType
	}
	ident, ok := expr.Owner.(*ast.Ident)
	if !ok {
		a.errorf(expr.Pos(), "new[...] owner must be a region name, tree store, or packed enum store, got %s", ownerType)
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
		a.errorf(expr.Pos(), "new[auto] needs an enclosing inferred region (the native stack arena); wrap it in `in auto:` (or a scope that already opens one)")
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

func stripTreeAllocOwnerExpr(expr ast.Expr) ast.Expr {
	for expr != nil {
		if paren, ok := expr.(*ast.ParenExpr); ok {
			expr = paren.Inner
			continue
		}
		return expr
	}
	return nil
}

func isTreeAllocPermExpr(expr ast.Expr) bool {
	ident, ok := stripTreeAllocOwnerExpr(expr).(*ast.Ident)
	return ok && ident != nil && ident.Name == "perm"
}

func (a *Analyzer) classifyTreeAllocOwnerExpr(expr ast.Expr) (treeAllocOwnerBinding, Type, bool) {
	if expr == nil {
		return treeAllocOwnerBinding{}, invalidType, false
	}
	if isTreeAllocPermExpr(expr) {
		return treeAllocOwnerBinding{Kind: treeAllocOwnerPerm}, nil, true
	}
	ownerType := a.analyzeExpr(expr)
	if storeType, ok := ownerType.(*TreeStoreType); ok && storeType != nil {
		return treeAllocOwnerBinding{Kind: treeAllocOwnerStore, StoreFamily: storeType.Family}, ownerType, true
	}
	arenaType := a.namedTypes["Arena"]
	if arenaType == nil {
		return treeAllocOwnerBinding{}, ownerType, false
	}
	stripped := stripTreeAllocOwnerExpr(expr)
	if ident, ok := stripped.(*ast.Ident); ok && SameType(ownerType, arenaType) {
		if regionSym, state := a.lookupRegionState(ident.Name); regionSym != nil {
			if state.Destroyed {
				a.errorf(expr.Pos(), "cannot allocate from destroyed region %q", ident.Name)
				return treeAllocOwnerBinding{}, ownerType, false
			}
			return treeAllocOwnerBinding{Kind: treeAllocOwnerRegion, RegionName: ident.Name}, ownerType, true
		}
		return treeAllocOwnerBinding{Kind: treeAllocOwnerArena}, ownerType, true
	}
	if refType, ok := ownerType.(*RefType); ok && refType != nil && SameType(refType.Elem, arenaType) {
		return treeAllocOwnerBinding{Kind: treeAllocOwnerArena}, ownerType, true
	}
	return treeAllocOwnerBinding{}, ownerType, false
}

func (a *Analyzer) requireActiveTreeConstructorOwner(pos lexer.Pos, treeType *TreeCategoryType, variant *EnumVariant) bool {
	if treeType == nil || variant == nil {
		return false
	}
	return a.requireActiveTreeFamilyConstructorOwner(pos, treeType.Family, treeType.Name+"."+variant.Name)
}

func (a *Analyzer) requireActiveTreeFamilyConstructorOwner(pos lexer.Pos, family *TreeType, constructorName string) bool {
	if family == nil {
		return false
	}
	switch a.currentTreeAllocOwner.Kind {
	case treeAllocOwnerPerm, treeAllocOwnerArena:
		return true
	case treeAllocOwnerRegion:
		if a.currentTreeAllocOwner.RegionName != "" {
			if regionSym, state := a.lookupRegionState(a.currentTreeAllocOwner.RegionName); regionSym == nil || state.Destroyed {
				a.errorf(pos, "tree constructor %q cannot allocate from destroyed region %q", constructorName, a.currentTreeAllocOwner.RegionName)
				return false
			}
		}
		return true
	case treeAllocOwnerStore:
		if a.currentTreeAllocOwner.StoreFamily != nil && family != a.currentTreeAllocOwner.StoreFamily {
			a.errorf(pos, "tree constructor %q requires active store %q, got active store for %q", constructorName, family.StoreType, a.currentTreeAllocOwner.StoreFamily.Name)
			return false
		}
		return true
	default:
		a.errorf(pos, "tree constructor %q requires an active in <owner>: scope or explicit new[owner]", constructorName)
		return false
	}
}

func (a *Analyzer) treeAllocConstructorInfo(expr ast.Expr) (*TreeCategoryType, *EnumVariant, *ast.CallExpr, bool) {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		treeType, variant, ok := a.treeConstructorInfoFromFieldExpr(n)
		if !ok {
			return nil, nil, nil, false
		}
		if variant != nil && len(variant.Payload) != 0 {
			return nil, nil, nil, false
		}
		return treeType, variant, nil, true
	case *ast.CallExpr:
		treeType, variant, ok := a.treeConstructorCall(n)
		return treeType, variant, n, ok
	default:
		return nil, nil, nil, false
	}
}

func (a *Analyzer) treeExactAllocConstructorInfo(expr ast.Expr) (Type, *ast.CallExpr, bool) {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		memberType, ok := a.treeExactMemberTypeFromFieldExpr(n)
		if !ok {
			return nil, nil, false
		}
		if len(treeExactMemberFieldDecls(memberType)) != 0 {
			return nil, nil, false
		}
		return memberType, nil, true
	case *ast.CallExpr:
		memberType, ok := a.treeExactMemberConstructorCall(n)
		if !ok {
			return nil, nil, false
		}
		return memberType, n, true
	default:
		return nil, nil, false
	}
}

func (a *Analyzer) analyzeTreeAllocExpr(expr *ast.AllocExpr, treeType *TreeCategoryType, variant *EnumVariant, callExpr *ast.CallExpr) Type {
	if treeType == nil {
		if expr != nil && expr.Value != nil {
			a.analyzeExpr(expr.Value)
		}
		return invalidType
	}
	if variant == nil {
		if callExpr != nil {
			for _, arg := range callExpr.Args {
				a.analyzeExpr(arg)
			}
		}
		return invalidType
	}
	if callExpr == nil {
		if treeType.Family != nil && treeType.Family.Decl != nil && len(treeType.Family.Decl.Common) != 0 {
			a.errorf(expr.Pos(), "tree constructor %q requires explicit common fields; use call syntax with named arguments", treeType.Name+"."+variant.Name)
			return invalidType
		}
		return treeType
	}
	return a.analyzeTreeConstructorCallExpr(callExpr, treeType, variant)
}

func (a *Analyzer) analyzeTreeExactAllocExpr(expr *ast.AllocExpr, memberType Type, callExpr *ast.CallExpr) Type {
	if memberType == nil {
		if expr != nil && expr.Value != nil {
			a.analyzeExpr(expr.Value)
		}
		return invalidType
	}
	if callExpr == nil {
		if len(treeExactMemberFieldDecls(memberType)) != 0 {
			a.errorf(expr.Pos(), "tree constructor %q requires explicit constructor arguments", memberType)
			return invalidType
		}
		return memberType
	}
	return a.analyzeTreeExactMemberConstructorCallExpr(callExpr, memberType)
}
