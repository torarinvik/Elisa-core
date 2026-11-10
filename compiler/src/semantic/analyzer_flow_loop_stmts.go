package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) analyzeCanStmt(stmt *ast.CanStmt) {
	refs := a.resolvePermissionRefs(stmt.Permissions, true)
	if permissionRefsContain(refs, "Unsafe", "PointerCast") {
		a.currentUnsafePointerCastGrantDepth++
		defer func() { a.currentUnsafePointerCastGrantDepth-- }()
	}
	handler := a.handlerForCan(stmt, refs)
	if handler != nil {
		refs = mergePermissionRefs(refs, handlerConcretePermissionRefs(handler))
	}
	if stmt.SuppressPermissionInference {
		// `trusted` is only an Unsafe tracking boundary. Global and other
		// effect rows still require a local `can` and must remain in caller rows.
		// Perf acknowledgements remain visible to their separate lint pass via
		// stmt.Permissions.
		refs = trustedUnsafePermissionRefs(refs)
	}
	if stmt.As != "" {
		a.analyzeCanCastStmt(stmt, refs)
		return
	}
	if !stmt.SuppressPermissionInference {
		// Acknowledgment-only grants (ComplexFlow, Scalar) silence local diagnostics
		// and must NOT surface in the function's inferred effects — nothing ever
		// REQUIRES them, so recording them would cascade can[…] obligations up
		// every caller chain (see registerBuiltinPermission's contract for both).
		a.recordFunctionPermissionRefs(withoutAcknowledgmentRefs(refs))
		// A tracked `can Unsafe.StaleRef:` grant discharges the forwarded-ref-store
		// check within its scope (like `trusted`), but — unlike `trusted` — the
		// recordFunctionPermissionRefs above surfaces Unsafe.StaleRef in the function's
		// effects, so the stored-borrow stays auditable through the capability system.
		grantsStaleRef := permissionRefsContain(refs, "Unsafe", "StaleRef")
		if grantsStaleRef {
			a.currentGrantedStaleRefDepth++
		}
		// `can Unsafe.NonProgress/AssumeProgress:` discharges a wrapped loop's progress obligation
		// like `trusted` (the grant is in scope for the loop), while still propagating via the
		// recordFunctionPermissionRefs above. Without this, converting a wrapping `trusted` to `can`
		// would silently re-flag the loop.
		grantsNonProgress := permissionRefsContain(refs, "Unsafe", "NonProgress")
		grantsAssumeProgress := permissionRefsContain(refs, "Unsafe", "AssumeProgress")
		if grantsNonProgress {
			a.currentGrantedNonProgressDepth++
		}
		if grantsAssumeProgress {
			a.currentGrantedAssumeProgressDepth++
		}
		a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
		if grantsNonProgress {
			a.currentGrantedNonProgressDepth--
		}
		if grantsAssumeProgress {
			a.currentGrantedAssumeProgressDepth--
		}
		if grantsStaleRef {
			a.currentGrantedStaleRefDepth--
		}
		return
	}
	granted := a.grantedPermissionRefs(refs)
	savedUsedPermissions := a.currentFunctionUsedPermissions
	savedUsedRefs := a.currentFunctionUsedPermissionRefs
	savedTrustedNonProgressDepth := a.currentTrustedNonProgressDepth
	savedTrustedAssumeProgressDepth := a.currentTrustedAssumeProgressDepth
	savedTrustedStaleRefDepth := a.currentTrustedStaleRefDepth
	if permissionRefsContain(refs, "Unsafe", "NonProgress") {
		a.currentTrustedNonProgressDepth++
	}
	if permissionRefsContain(refs, "Unsafe", "AssumeProgress") {
		a.currentTrustedAssumeProgressDepth++
	}
	if permissionRefsContain(refs, "Unsafe", "StaleRef") {
		a.currentTrustedStaleRefDepth++
	}
	if permissionRefsContain(refs, "Unsafe", "BlockMain") && a.currentProgressSummary != nil {
		a.currentProgressSummary.HasUnsafeBlockMain = true
	}
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
	bodyRefs := canonicalizePermissionRefs(a.currentFunctionUsedPermissionRefs)
	remainingRefs := missingGrantedPermissionRefs(bodyRefs, granted)
	a.currentFunctionUsedPermissions = savedUsedPermissions
	a.currentFunctionUsedPermissionRefs = savedUsedRefs
	a.currentTrustedNonProgressDepth = savedTrustedNonProgressDepth
	a.currentTrustedAssumeProgressDepth = savedTrustedAssumeProgressDepth
	a.currentTrustedStaleRefDepth = savedTrustedStaleRefDepth
	a.recordFunctionPermissionRefs(remainingRefs)
}

// analyzeCanCastStmt handles a checked `can X as Y:` block (Phase 4). It verifies
// the cast is sound (Y subsumes every member of X in the lattice), grants X to the
// body so X-uses are discharged locally, and surfaces Y — not the used members — as
// the block's contribution to the function's inferred can[…].
func (a *Analyzer) analyzeCanCastStmt(stmt *ast.CanStmt, refs []ast.PermissionRef) {
	target := stmt.As
	_, _, isPerm := a.lookupVisiblePermission(target)
	_, isAlias := a.lookupVisibleCapabilityAlias(target)
	if !isPerm && !isAlias {
		a.errorf(stmt.Position, "%s", UnknownPermissionMessage(target))
	} else {
		covered := a.castTargetCoverage(target)
		for _, x := range refs {
			if !permissionRefGranted(x, covered) {
				a.errorf(stmt.Position, "`as %s` is unsound: %q does not subsume %q; declare `permission %s: includes %s` (or add it to the `alias %s` set) or use `trusted`", target, target, permissionRefKey(x), target, x.Name, target)
			}
		}
	}

	granted := a.grantedPermissionRefs(refs)
	savedUsedPermissions := a.currentFunctionUsedPermissions
	savedUsedRefs := a.currentFunctionUsedPermissionRefs
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
	bodyRefs := canonicalizePermissionRefs(a.currentFunctionUsedPermissionRefs)
	remainingRefs := missingGrantedPermissionRefs(bodyRefs, granted)
	a.currentFunctionUsedPermissions = savedUsedPermissions
	a.currentFunctionUsedPermissionRefs = savedUsedRefs
	a.recordFunctionPermissionRefs(remainingRefs)
	// Surface the cast target as the block's inferred requirement. For an alias
	// this is a bare alias ref, which expands to its member refs wherever the
	// resulting function's effects are later resolved.
	a.recordFunctionPermissionRefs([]ast.PermissionRef{{Position: stmt.Position, Name: target}})
}

// castTargetCoverage returns the set of permission keys subsumed by a checked-cast
// target `as <target>`. For a permission family the coverage is the family plus its
// transitive `includes` closure (and, via permissionRefGranted, every member of those
// families). For a capability `alias` the coverage is the alias's fully expanded
// member refs plus the include-closure of any whole families among them, so a wider
// alias soundly subsumes the narrower requirement it is cast over.
func (a *Analyzer) castTargetCoverage(target string) map[string]bool {
	covered := map[string]bool{target: true}
	if _, _, ok := a.lookupVisiblePermission(target); ok {
		a.markSubsumedFamilies(target, covered)
		return covered
	}
	if aliasRefs, ok := a.lookupVisibleCapabilityAlias(target); ok {
		for _, ref := range a.expandCapabilityAliases(aliasRefs) {
			if ref.Name == "" {
				continue
			}
			covered[permissionRefKey(ref)] = true
			if ref.Member == "" {
				// A whole-family member of the alias also drags in that family's
				// include-closure.
				covered[ref.Name] = true
				a.markSubsumedFamilies(ref.Name, covered)
			}
		}
	}
	return covered
}

func (a *Analyzer) analyzePoolStmt(stmt *ast.PoolStmt) {
	var poolType Type
	var borrowedPoolSym *Symbol
	var borrowedPoolKey affineValueKey
	var hasBorrowedPoolKey bool
	if stmt.BorrowedPool != "" {
		sym, ok := a.currentScope.Lookup(stmt.BorrowedPool)
		if !ok || sym == nil || (sym.Kind != SymbolLocal && sym.Kind != SymbolParam && sym.Kind != SymbolGlobal) {
			a.errorf(stmt.Position, "borrowed nursery requires a mutable ThreadPool binding %q", stmt.BorrowedPool)
		} else if ref, isRef := sym.Type.(*RefType); isRef {
			if ref == nil || !ref.Mutable || !isBuiltinProtocolOwnerType(ref.Elem, "ThreadPool") {
				a.errorf(stmt.Position, "borrowed nursery requires %q to be a mutable ThreadPool reference", stmt.BorrowedPool)
			} else {
				poolType = ref.Elem
				borrowedPoolSym = sym
			}
		} else if !sym.Mutable || !isBuiltinProtocolOwnerType(sym.Type, "ThreadPool") {
			a.errorf(stmt.Position, "borrowed nursery requires %q to be a mutable ThreadPool value or reference", stmt.BorrowedPool)
		} else {
			poolType = sym.Type
			borrowedPoolSym = sym
		}
		if borrowedPoolSym != nil {
			borrowedPoolKey, hasBorrowedPoolKey = a.lookupProtocolTargetKey(&ast.Ident{Position: stmt.Position, Name: stmt.BorrowedPool})
		}
	} else {
		poolCall := &ast.CallExpr{
			Position: stmt.Position,
			Func:     &ast.Ident{Position: stmt.Position, Name: "pool_new"},
			Args:     []ast.Expr{stmt.Workers},
		}
		poolType = a.analyzeExpr(poolCall)
	}
	savedScope := a.currentScope
	savedRegions := a.currentRegions
	savedRegionsBorrowed := a.currentRegionsBorrowed
	savedRegionMarks := a.currentRegionMarks
	savedCheckpoints := a.currentCheckpoints
	savedRegionRefs := a.currentRegionRefs
	savedRegionRefsBorrowed := a.currentRegionRefsBorrowed
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedPools := a.currentPoolScopes
	a.currentScope = NewScope(savedScope)
	a.borrowRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentCheckpoints = a.cloneCheckpointStates()
	a.borrowRegionRefStates()
	a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	outerRegions := make(map[*Symbol]int, len(savedRegions))
	for region, state := range savedRegions {
		if !state.Destroyed {
			outerRegions[region] = state.Generation
		}
	}
	joined := []poolJoinedRegionDependency{}
	joinedViews := []poolJoinedView{}
	borrowedAlias := &Symbol{Name: stmt.Name, Kind: SymbolLocal, Type: poolType, Node: stmt, Mutable: true}
	a.currentPoolScopes = append(append([]poolScopeState(nil), savedPools...), poolScopeState{Name: stmt.Name, Node: stmt, BorrowedPool: borrowedPoolSym, BorrowedPoolKey: borrowedPoolKey, HasBorrowedPoolKey: hasBorrowedPoolKey, BorrowedAlias: borrowedAlias, OuterRegions: outerRegions, Joined: &joined, JoinedViews: &joinedViews})
	a.defineLocal(borrowedAlias, stmt.Pos())
	for _, inner := range stmt.Body {
		a.analyzeStmt(inner)
	}
	for _, dep := range joined {
		if state, ok := a.currentRegions[dep.Region]; !ok || state.Destroyed || state.Generation != dep.Generation {
			a.errorf(dep.Pos, threadLocalRegionDependencyMessage(dep.CallName, dep.Region.Name))
		}
	}
	// A submitted view is live until the join: report it at the submit site if its backing
	// was invalidated anywhere later in the body.
	for _, view := range joinedViews {
		sym := view.Sym
		if sym == nil {
			if found, ok := a.currentScope.Lookup(view.Name); ok {
				sym = found
			}
		}
		if sym == nil || a.currentStorageViewDeps == nil {
			continue
		}
		if dep, ok := a.currentStorageViewDeps[sym]; ok && !dep.Valid {
			a.flagStaleStorageViewUse(view.Expr, view.Name, dep)
		}
		if view.Sym != nil {
			delete(a.currentStorageViewDeps, view.Sym)
		}
	}
	a.currentScope = savedScope
	a.currentRegions = savedRegions
	a.currentRegionsBorrowed = savedRegionsBorrowed
	a.currentRegionMarks = savedRegionMarks
	a.currentCheckpoints = savedCheckpoints
	a.currentRegionRefs = savedRegionRefs
	a.currentRegionRefsBorrowed = savedRegionRefsBorrowed
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
	a.currentPoolScopes = savedPools
}

func (a *Analyzer) analyzeForStmt(stmt *ast.ForStmt) {
	if stmt.Reverse {
		a.errorf(stmt.Pos(), "reverse range for loops are not supported yet; reverse iterable loops are")
	}
	// A function that `fulfills Vectorizes` (docs/89 Stage 5, measure class) opts its range loops into
	// the autovec verifier: tag the loop expected-to-vectorize so the post-optimization pass warns if
	// it didn't. Verify-only — the tag changes no codegen, just the -Wperf measurement.
	if a.currentFunctionExpectsVectorize && !stmt.AutovecExpected {
		stmt.AutovecExpected = true
		stmt.AutovecReason = "fulfills Vectorizes"
	}
	startType := a.analyzeExpr(stmt.Start)
	endType := a.analyzeExpr(stmt.End)
	loopType := CommonNumericType(startType, endType)
	if !IsIntegralType(loopType) {
		a.errorf(stmt.Pos(), "for loop range requires integral bounds, got %s and %s", startType, endType)
		loopType = invalidType
	}
	if stmt.Step != nil {
		stepType := a.analyzeExpr(stmt.Step)
		loopType = CommonNumericType(loopType, stepType)
		if !IsIntegralType(stepType) || !IsIntegralType(loopType) {
			a.errorf(stmt.Step.Pos(), "for loop range step must be integral, got %s", stepType)
			loopType = invalidType
		}
		if value, ok := a.evalConstExpr(stmt.Step); ok && value.Kind == ConstInt && value.Int == 0 {
			a.errorf(stmt.Step.Pos(), "for loop range step cannot be zero")
		}
	}
	if stmt.Op != lexer.TOKEN_RANGE && stmt.Op != lexer.TOKEN_RANGE_LT && stmt.Op != lexer.TOKEN_RANGE_GT {
		a.errorf(stmt.Pos(), "for loop uses unsupported range operator %s", lexer.TokenName(stmt.Op))
	}

	loopScope := NewScope(a.currentScope)
	loopSym := &Symbol{Name: stmt.Name, Kind: SymbolLocal, Type: loopType, Node: stmt, Mutable: false}
	a.defineLocalInScope(loopScope, loopSym, stmt.Pos())

	// Seed a numRange fact for the loop variable in the loop body scope so that
	// refinement proofs (law constraints, where-predicates) over the loop variable
	// can discharge statically. For `for i in s..<e:` the loop variable satisfies
	// the closed interval [s, e-1]; we record whatever const-evaluable portion of
	// that interval we can prove:
	//   - both bounds known  → tight [s, e-1] fact
	//   - only start known   → half-open [s, ∞) lo-only fact
	//   - only end known     → half-open (-∞, e-1] hi-only fact
	//
	// Sound because the loop variable is immutable so it cannot be re-assigned in
	// the body, and the loop body only executes when start < end (empty range →
	// body never runs), so the body always sees a value in [s, e-1].
	//
	// Note: `..=` (inclusive) desugars to `..<(end+1)` at parse time, so by the
	// time we get here it is always TOKEN_RANGE_LT.  The hi fact will be
	// `(orig_end+1)-1 = orig_end`, which is the correct inclusive upper bound.
	if stmt.Op == lexer.TOKEN_RANGE_LT {
		var rf numRange
		hasRf := false
		if sv, startConst := a.constIntValue(stmt.Start); startConst {
			rf.loKnown = true
			rf.lo = sv
			hasRf = true
		}
		if ev, endConst := a.constIntValue(stmt.End); endConst {
			rf.hiKnown = true
			rf.hi = ev - 1
			hasRf = true
		}
		if hasRf {
			if loopScope.rangeFacts == nil {
				loopScope.rangeFacts = map[string]numRange{}
			}
			loopScope.rangeFacts[stmt.Name] = rf
		}
	}

	a.recordForRangeSMTFact(stmt, loopScope, loopType)

	savedIndexBounds, savedIndexBoundsShared := a.currentIndexBounds, a.indexBoundsShared
	savedBoundEqual, savedBoundEqualShared := a.currentBoundEqual, a.boundEqualShared
	savedViewStaticLen := a.currentViewStaticLen
	savedViewMutable := a.currentViewMutable
	a.currentViewStaticLen = cloneViewStaticLen(savedViewStaticLen)
	a.currentViewMutable = cloneViewMutable(savedViewMutable)
	a.currentBoundEqual = cloneBoundEqual(savedBoundEqual)
	a.boundEqualShared = false
	loopIndexBounds := cloneIndexBoundFacts(savedIndexBounds)
	if stmt.Op == lexer.TOKEN_RANGE_LT && isZeroOptimizationExpr(stmt.Start) {
		if loopIndexBounds == nil {
			loopIndexBounds = map[string]indexBoundFact{}
		}
		loopIndexBounds[stmt.Name] = indexBoundFact{Upper: optimizationExprString(stmt.End), NonNeg: true}
	}
	a.currentIndexBounds = loopIndexBounds
	a.indexBoundsShared = false

	mergedAffine := a.cloneAffineValueStates()
	mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
	mergedFunctionValues := a.cloneFunctionValueBindings()
	mergedSpecializedValueTypes := a.cloneSpecializedValueTypeBindings()
	derivedEntry := a.captureDerivedLoopEntry(a.currentScope)
	mergedStorageViewDeps := a.cloneStorageViewDeps()
	entryAffine := a.cloneAffineValueStates()
	outerScope := a.currentScope
	// Classify the entry range before checking mutations in the body. Restrict
	// this exclusion to the built-in ascending, default-step range.
	_, emptyRange, _ := a.classifyDerivedLiteralRange(stmt)
	// Inductive range-loop invariants, proven on the pristine pre-loop facts (analyzer_for_range_invariants.go).
	forInvariants, forExitFacts := a.proveForRangeInvariants(stmt, loopType)
	a.havocLoopWrittenFacts(stmt.Body, nil)
	outerInvariantBase := len(a.activeOuterLoopInvariants)
	a.activeOuterLoopInvariants = append(a.activeOuterLoopInvariants, forInvariants...)
	a.pushLoopAffineFrame()
	a.pushStorageViewLoopUseFrame(outerScope, stmt.Body)
	a.loopDepth++
	bodySnapshot := a.analyzeBlockWithAffineClone(stmt.Body, loopScope)
	a.activeOuterLoopInvariants = a.activeOuterLoopInvariants[:outerInvariantBase]
	a.loopDepth--
	a.checkStorageViewLoopBackEdge(bodySnapshot.StorageViewDeps, blockDefinitelyExits(stmt.Body))
	if !emptyRange {
		mergedFunctionValues = a.mergeLoopJumpFunctionValues(mergedFunctionValues)
		mergedSpecializedValueTypes = a.mergeLoopJumpSpecializedTypes(mergedSpecializedValueTypes)
	}
	continuedAffine := a.finishLoopAffineFrame(entryAffine, bodySnapshot.Affine, blockDefinitelyExits(stmt.Body), outerScope, stmt.Pos())
	a.currentIndexBounds, a.indexBoundsShared = savedIndexBounds, savedIndexBoundsShared
	a.currentBoundEqual, a.boundEqualShared = savedBoundEqual, savedBoundEqualShared
	a.currentViewStaticLen = savedViewStaticLen
	a.currentViewMutable = savedViewMutable
	if !blockDefinitelyExits(stmt.Body) {
		mergedAffine = mergeAffineValueStates(mergedAffine, bodySnapshot.Affine)
		mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, bodySnapshot.BorrowedOwnerRefs)
		mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, bodySnapshot.FunctionValues)
		if !emptyRange {
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, bodySnapshot.SpecializedValueTypes)
		}
		// A storage-view interior reference invalidated by a mutation inside the loop body (e.g. a
		// darray push or relocating dict insert) stays invalid after the loop — the body may have
		// run. Without this merge the invalidation was discarded at block exit, leaving a stale
		// reference usable after the loop (use-after-realloc).
		mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, bodySnapshot.StorageViewDeps)
	}
	mergedAffine = mergeAffineValueStates(mergedAffine, continuedAffine)
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	if exit := a.checkDerivedLoopTransfer(stmt.Body, nil, stmt, derivedEntry, stmt.Pos()); exit != nil {
		for root, typ := range exit {
			a.bindTrackedValueType(root, typ)
		}
	}
	a.currentStorageViewDeps = mergedStorageViewDeps
	for _, fact := range forExitFacts {
		a.recordSMTAssertFact(fact)
	}
	delete(a.privateFreshDArrayCache, a.currentFuncDecl)
	a.maybeAutoReserveCountingFill(stmt)
}

type iterLoopSourceInfo struct {
	ItemType        Type
	AllowRef        bool
	AllowMutableRef bool
	ItemFacts       OptimizationFacts
	HasItemFacts    bool
}
