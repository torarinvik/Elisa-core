package semantic

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) analyzeCanStmt(stmt *ast.CanStmt) {
	refs := a.resolvePermissionRefs(stmt.Permissions, true)
	if stmt.As != "" {
		a.analyzeCanCastStmt(stmt, refs)
		return
	}
	if !stmt.SuppressPermissionInference {
		a.recordFunctionPermissionRefs(refs)
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
	poolCall := &ast.CallExpr{
		Position: stmt.Position,
		Func:     &ast.Ident{Position: stmt.Position, Name: "pool_new"},
		Args:     []ast.Expr{stmt.Workers},
	}
	poolType := a.analyzeExpr(poolCall)
	savedScope := a.currentScope
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedCheckpoints := a.currentCheckpoints
	savedRegionRefs := a.currentRegionRefs
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedPools := a.currentPoolScopes
	a.currentScope = NewScope(savedScope)
	a.currentRegions = a.cloneRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentCheckpoints = a.cloneCheckpointStates()
	a.currentRegionRefs = a.cloneRegionRefStates()
	a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	a.currentPoolScopes = append(append([]poolScopeState(nil), savedPools...), poolScopeState{Name: stmt.Name})
	a.defineLocal(&Symbol{Name: stmt.Name, Kind: SymbolLocal, Type: poolType, Node: stmt, Mutable: true}, stmt.Pos())
	for _, inner := range stmt.Body {
		a.analyzeStmt(inner)
	}
	a.currentScope = savedScope
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentCheckpoints = savedCheckpoints
	a.currentRegionRefs = savedRegionRefs
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

	savedIndexBounds := a.currentIndexBounds
	savedBoundEqual := a.currentBoundEqual
	savedViewStaticLen := a.currentViewStaticLen
	savedViewMutable := a.currentViewMutable
	a.currentViewStaticLen = cloneViewStaticLen(savedViewStaticLen)
	a.currentViewMutable = cloneViewMutable(savedViewMutable)
	a.currentBoundEqual = cloneBoundEqual(savedBoundEqual)
	loopIndexBounds := cloneIndexBoundFacts(savedIndexBounds)
	if stmt.Op == lexer.TOKEN_RANGE_LT && isZeroOptimizationExpr(stmt.Start) {
		if loopIndexBounds == nil {
			loopIndexBounds = map[string]indexBoundFact{}
		}
		loopIndexBounds[stmt.Name] = indexBoundFact{Upper: optimizationExprString(stmt.End), NonNeg: true}
	}
	a.currentIndexBounds = loopIndexBounds

	mergedAffine := a.cloneAffineValueStates()
	mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
	mergedFunctionValues := a.cloneFunctionValueBindings()
	mergedSpecializedValueTypes := a.cloneSpecializedValueTypeBindings()
	mergedStorageViewDeps := a.cloneStorageViewDeps()
	a.loopDepth++
	bodySnapshot := a.analyzeBlockWithAffineClone(stmt.Body, loopScope)
	a.loopDepth--
	a.currentIndexBounds = savedIndexBounds
	a.currentBoundEqual = savedBoundEqual
	a.currentViewStaticLen = savedViewStaticLen
	a.currentViewMutable = savedViewMutable
	if !blockDefinitelyExits(stmt.Body) {
		mergedAffine = mergeAffineValueStates(mergedAffine, bodySnapshot.Affine)
		mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, bodySnapshot.BorrowedOwnerRefs)
		mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, bodySnapshot.FunctionValues)
		mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, bodySnapshot.SpecializedValueTypes)
		// A storage-view interior reference invalidated by a mutation inside the loop body (e.g. a
		// darray push or relocating dict insert) stays invalid after the loop — the body may have
		// run. Without this merge the invalidation was discarded at block exit, leaving a stale
		// reference usable after the loop (use-after-realloc).
		mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, bodySnapshot.StorageViewDeps)
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	a.currentStorageViewDeps = mergedStorageViewDeps
	a.maybeAutoReserveCountingFill(stmt)
}

type iterLoopSourceInfo struct {
	ItemType        Type
	AllowRef        bool
	AllowMutableRef bool
	ItemFacts       OptimizationFacts
	HasItemFacts    bool
}

func (a *Analyzer) inferIterLoopItemOptimizationFacts(sourceExpr ast.Expr, sourceType Type) (OptimizationFacts, bool) {
	if a == nil || sourceExpr == nil || sourceType == nil {
		return OptimizationFacts{}, false
	}
	switch tt := sourceType.(type) {
	case *GenericInstanceType:
		if _, ok := ChunksExactViewItemType(tt); ok {
			return a.inferChunksExactItemOptimizationFacts(&ast.IndexExpr{
				Position: sourceExpr.Pos(),
				Object:   sourceExpr,
				Index:    &ast.Ident{Position: sourceExpr.Pos(), Name: "__iter_index"},
			})
		}
	}
	return OptimizationFacts{}, false
}

func (a *Analyzer) resolveIterLoopSourceInfo(sourceExpr ast.Expr, sourceType Type) (iterLoopSourceInfo, bool) {
	if sourceType == nil {
		return iterLoopSourceInfo{}, false
	}
	// Column scan `Expr of .field` yields computed scalars, not addressable
	// elements — value binding only, no ref/mutable-ref.
	if _, ok := sourceExpr.(*ast.EnumColumnExpr); ok {
		if view, ok := StripAggregateStateType(sourceType).(*ViewType); ok {
			return iterLoopSourceInfo{ItemType: view.Elem}, true
		}
		return iterLoopSourceInfo{ItemType: invalidType}, false
	}
	facts, hasFacts := a.exprFacts[sourceExpr]
	readOnly := hasFacts && facts.ReadOnly
	switch tt := sourceType.(type) {
	case *ConstValueType:
		if tt.Value.Kind != ConstList && tt.Value.Kind != ConstTuple {
			return iterLoopSourceInfo{}, false
		}
		itemValue := ConstValue{Kind: ConstUnknown}
		if len(tt.Value.Elems) != 0 {
			itemValue = tt.Value.Elems[0]
		}
		return iterLoopSourceInfo{ItemType: &ConstValueType{Value: itemValue}}, true
	case *ArrayType:
		return iterLoopSourceInfo{ItemType: tt.Elem, AllowRef: true, AllowMutableRef: true}, true
	case *DArrayType:
		return iterLoopSourceInfo{ItemType: tt.Elem, AllowRef: true, AllowMutableRef: !readOnly}, true
	case *ViewType:
		if tt.SurfaceName != "" && tt.SurfaceName != "view" {
			return iterLoopSourceInfo{}, false
		}
		return iterLoopSourceInfo{ItemType: tt.Elem, AllowRef: true, AllowMutableRef: !readOnly}, true
	case *StoreRowsViewType:
		return iterLoopSourceInfo{ItemType: &StoreRowViewType{Store: tt.Store}}, true
	case *DStrType:
		return iterLoopSourceInfo{ItemType: a.namedTypes["char"]}, true
	case *SViewType:
		return iterLoopSourceInfo{ItemType: a.namedTypes["char"]}, true
	case *GenericInstanceType:
		if itemType, ok := EnumerateViewItemType(tt); ok {
			return iterLoopSourceInfo{ItemType: itemType}, true
		}
		if itemType, ok := TreeKindFilteredViewItemType(tt); ok {
			return iterLoopSourceInfo{ItemType: itemType}, true
		}
		if itemType, ok := TreeChildrenItemType(tt); ok {
			return iterLoopSourceInfo{ItemType: itemType}, true
		}
		if itemType, ok := TreeAttributeSequenceItemType(tt); ok {
			return iterLoopSourceInfo{ItemType: itemType}, true
		}
		if itemType, ok := ChunksExactViewItemType(tt); ok {
			info := iterLoopSourceInfo{ItemType: itemType}
			if itemFacts, ok := a.inferIterLoopItemOptimizationFacts(sourceExpr, sourceType); ok {
				info.ItemFacts = itemFacts
				info.HasItemFacts = true
			}
			return info, true
		}
		return iterLoopSourceInfo{}, false
	case *StructType:
		if isRuntimeStringViewType(tt) {
			return iterLoopSourceInfo{ItemType: a.namedTypes["char"]}, true
		}
		return iterLoopSourceInfo{}, false
	case *RefType:
		if tt.State != RefStateNonNull {
			a.errorf(sourceExpr.Pos(), "iterable for loop requires a proven non-null reference source, got %s", sourceType)
			return iterLoopSourceInfo{}, false
		}
		info, ok := a.resolveIterLoopSourceInfo(sourceExpr, tt.Elem)
		if !ok {
			return iterLoopSourceInfo{}, false
		}
		if !tt.Mutable {
			info.AllowMutableRef = false
		}
		return info, true
	default:
		return iterLoopSourceInfo{}, false
	}
}

func iterLoopRefType(itemType Type, mutable bool) Type {
	return &RefType{Elem: itemType, Mutable: mutable, State: RefStateNonNull, Storage: RefStorageAny}
}

func (a *Analyzer) bindIterLoopPattern(scope *Scope, pattern ast.MoveBindPattern, mode ast.IterBindMode, itemType Type, itemFacts OptimizationFacts, hasItemFacts bool) bool {
	if scope == nil || pattern == nil {
		return false
	}
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() {
		a.currentScope = savedScope
	}()
	bindingTypeFor := func(pos lexer.Pos, fieldName string, fieldType Type, fieldMutable bool) Type {
		if mode == ast.IterBindValue {
			return fieldType
		}
		if mode == ast.IterBindMutableRef && !fieldMutable {
			a.errorf(pos, "for mutable ref destructuring requires mutable field %q", fieldName)
			return invalidType
		}
		return iterLoopRefType(fieldType, mode == ast.IterBindMutableRef)
	}
	switch p := pattern.(type) {
	case *ast.MoveBindNamePattern:
		if p.Name == "_" {
			return true
		}
		sym := &Symbol{Name: p.Name, Kind: SymbolLocal, Type: bindingTypeFor(p.Pos(), p.Name, itemType, true), Node: p, Mutable: false}
		a.defineLocal(sym, p.Pos())
		if mode == ast.IterBindValue && hasItemFacts && a.symbolFacts != nil {
			a.symbolFacts[sym] = itemFacts
		}
		return true
	case *ast.MoveBindStructPattern:
		fields, ok := a.resolveMoveBindStructPattern(p, itemType)
		if !ok {
			return false
		}
		for i, arg := range p.Args {
			if i >= len(fields) || arg.Name == "_" {
				continue
			}
			sym := &Symbol{Name: arg.Name, Kind: SymbolLocal, Type: bindingTypeFor(arg.Position, fields[i].Name, fields[i].Type, fields[i].Mutable), Node: p, Mutable: false}
			a.defineLocal(sym, arg.Position)
		}
		return true
	case *ast.MoveBindTuplePattern:
		tupleType, ok := StripAggregateStateType(itemType).(*TupleType)
		if !ok || tupleType == nil {
			a.errorf(p.Pos(), "iterable for tuple pattern requires a tuple item, got %s", itemType)
			return false
		}
		if len(p.Args) != len(tupleType.Fields) {
			a.errorf(p.Pos(), "iterable tuple pattern expects %d bindings, got %d", len(tupleType.Fields), len(p.Args))
		}
		limit := len(p.Args)
		if len(tupleType.Fields) < limit {
			limit = len(tupleType.Fields)
		}
		for i := 0; i < limit; i++ {
			arg := p.Args[i]
			if arg.Name == "_" {
				continue
			}
			fieldName := tupleType.Fields[i].Name
			if fieldName == "" {
				fieldName = fmt.Sprintf("_%d", i)
			}
			sym := &Symbol{Name: arg.Name, Kind: SymbolLocal, Type: bindingTypeFor(arg.Position, fieldName, tupleType.Fields[i].Type, false), Node: p, Mutable: false}
			a.defineLocal(sym, arg.Position)
		}
		return true
	case *ast.MoveBindVariantPattern:
		a.errorf(p.Pos(), "iterable for loop pattern must be irrefutable; variant patterns are not supported here")
		return false
	default:
		a.errorf(pattern.Pos(), "unsupported iterable for pattern %T", pattern)
		return false
	}
}

func (a *Analyzer) analyzeIterForStmt(stmt *ast.IterForStmt) {
	sourceType := a.analyzeExpr(stmt.Source)
	info, ok := a.resolveIterLoopSourceInfo(stmt.Source, sourceType)
	if !ok {
		a.errorf(stmt.Source.Pos(), "iterable for loop currently requires an array, dynamic array, view, store.rows(), frozen tree row view, string-like iterable, ChunksExactView, source.enumerate(), children(node), or a projected tree attribute sequence, got %s", sourceType)
		info.ItemType = invalidType
	}
	a.maybeAutoReserveIterFill(stmt, sourceType)
	if stmt.MovedSource {
		// A move-drain is the sanctioned way to consume affine elements: it moves each
		// element OUT and requires the body to consume it. It is only sound over a container
		// the current function OWNS outright — draining through a borrow (`darray&` param) or
		// a field/element of borrowed storage would consume the *caller's* elements behind its
		// back. So require a directly-named, non-reference darray local / by-value param; a
		// field or element must be moved into a local first.
		if _, isRef := sourceType.(*RefType); isRef {
			a.errorf(stmt.Pos(), "cannot `for ... in move` a borrowed container (%s); move it into a local you own first", sourceType)
		} else if _, isDArray := stripRefForBounds(sourceType).(*DArrayType); !isDArray {
			a.errorf(stmt.Pos(), "`for ... in move` (drain) currently requires a darray source, got %s", sourceType)
		} else if _, isIdent := stmt.Source.(*ast.Ident); !isIdent {
			a.errorf(stmt.Pos(), "`for ... in move` (drain) source must be a darray you own directly; move a field/element into a local first")
		}
		if moveDrainBodyHasNonLocalExit(stmt.Body) {
			a.errorf(stmt.Pos(), "a `for ... in move` drain must consume every element; `break`/`return` inside the drain body would leak the remaining un-consumed elements")
		}
	}
	if stmt.Mode == ast.IterBindValue && !stmt.MovedSource && a.containsAffineHandleValues(info.ItemType, map[string]bool{}) {
		a.errorf(stmt.Pos(), "for value iteration does not support affine element type %s; use ref or mutable ref (or `for x in move c` to drain and consume each element)", info.ItemType)
	}
	if stmt.Mode != ast.IterBindValue && a.containsAffineHandleValues(info.ItemType, map[string]bool{}) && !isBorrowableAffineOwnerType(info.ItemType) {
		a.errorf(stmt.Pos(), "references to values containing linear handles are not supported; got %s&", info.ItemType)
	}
	switch stmt.Mode {
	case ast.IterBindRef:
		if !info.AllowRef {
			a.errorf(stmt.Pos(), "for ref requires an addressable array-like iterable, got %s", sourceType)
		}
	case ast.IterBindMutableRef:
		if !info.AllowMutableRef {
			a.errorf(stmt.Pos(), "for mutable ref requires a writable addressable array-like iterable, got %s", sourceType)
		}
	}

	loopScope := NewScope(a.currentScope)
	a.bindIterLoopPattern(loopScope, stmt.Pattern, stmt.Mode, info.ItemType, info.ItemFacts, info.HasItemFacts)
	var movedElemSym *Symbol
	if stmt.MovedSource {
		// Register the moved element as a per-iteration must-consume value (a no-op when the
		// element is not affine). The body is then required to consume it before the iteration
		// ends, transferring the container's aggregate obligation into per-element obligations.
		if namePattern, ok := stmt.Pattern.(*ast.MoveBindNamePattern); ok && namePattern.Name != "_" {
			if sym, ok := loopScope.Lookup(namePattern.Name); ok {
				movedElemSym = sym
				a.trackAffineValueSymbol(sym)
			}
		} else if _, isName := stmt.Pattern.(*ast.MoveBindNamePattern); !isName {
			a.errorf(stmt.Pos(), "`for ... in move` (drain) currently requires a single loop variable, not a destructuring pattern")
		}
	}
	if stmt.PatternFilter != nil {
		var valueExpr ast.Expr
		patternType := info.ItemType
		if stmt.PatternFilterSubject != "" {
			if sym, ok := loopScope.Lookup(stmt.PatternFilterSubject); ok {
				patternType = sym.Type
				valueExpr = &ast.Ident{Position: stmt.PatternFilter.Pos(), Name: stmt.PatternFilterSubject}
			} else {
				a.errorf(stmt.PatternFilter.Pos(), "for where pattern filter subject %q is not bound by the loop pattern", stmt.PatternFilterSubject)
			}
		} else if namePattern, ok := stmt.Pattern.(*ast.MoveBindNamePattern); ok && namePattern.Name != "_" {
			valueExpr = &ast.Ident{Position: namePattern.Pos(), Name: namePattern.Name}
		}
		a.analyzeNestedMatchPattern(stmt.PatternFilter, patternType, valueExpr, loopScope)
	}
	if stmt.WhereFilter != nil {
		condType := a.analyzeCondExprInScope(stmt.WhereFilter, loopScope)
		if !IsBoolType(condType) {
			a.errorf(stmt.WhereFilter.Pos(), "for where filter must be bool, got %s", condType)
		}
	}
	if stmt.Filter != nil {
		condType := a.analyzeCondExprInScope(stmt.Filter, loopScope)
		if !IsBoolType(condType) {
			a.errorf(stmt.Filter.Pos(), "for filter must be bool, got %s", condType)
		}
	}

	mergedAffine := a.cloneAffineValueStates()
	mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
	mergedFunctionValues := a.cloneFunctionValueBindings()
	mergedSpecializedValueTypes := a.cloneSpecializedValueTypeBindings()
	mergedStorageViewDeps := a.cloneStorageViewDeps()
	var bodySnapshot affineFlowSnapshot
	// Lock a relocatable darray iterand for the loop body: a push/extend/reserve/clear that
	// moves its buffer mid-iteration is the iterator-invalidation bug. A stable backing
	// (reserve_commit/fixed) never relocates, so it is left unlocked (mutation there is safe).
	iterLockKey := ""
	if _, isDArray := stripRefForBounds(sourceType).(*DArrayType); isDArray && !a.containerBackingIsStable(stmt.Source) {
		iterLockKey = optimizationExprString(stmt.Source)
	}
	var iterLockPrior lexer.Pos
	iterLockHadPrior := false
	if iterLockKey != "" {
		if a.currentIteratedSources == nil {
			a.currentIteratedSources = map[string]lexer.Pos{}
		}
		iterLockPrior, iterLockHadPrior = a.currentIteratedSources[iterLockKey]
		a.currentIteratedSources[iterLockKey] = stmt.Pos()
	}
	a.loopDepth++
	if stmt.Filter != nil {
		bodySnapshot = a.analyzeBlockWithConditionAffineClone(stmt.Body, loopScope, stmt.Filter, true)
	} else if stmt.WhereFilter != nil {
		bodySnapshot = a.analyzeBlockWithConditionAffineClone(stmt.Body, loopScope, stmt.WhereFilter, true)
	} else {
		bodySnapshot = a.analyzeBlockWithAffineClone(stmt.Body, loopScope)
	}
	a.loopDepth--
	if iterLockKey != "" {
		if iterLockHadPrior {
			a.currentIteratedSources[iterLockKey] = iterLockPrior
		} else {
			delete(a.currentIteratedSources, iterLockKey)
		}
	}
	if !blockDefinitelyExits(stmt.Body) {
		mergedAffine = mergeAffineValueStates(mergedAffine, bodySnapshot.Affine)
		mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, bodySnapshot.BorrowedOwnerRefs)
		mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, bodySnapshot.FunctionValues)
		mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, bodySnapshot.SpecializedValueTypes)
		// Propagate interior-reference invalidations out of the loop (see the range-loop merge).
		mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, bodySnapshot.StorageViewDeps)
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
	a.currentStorageViewDeps = mergedStorageViewDeps
	if stmt.MovedSource {
		if movedElemSym != nil {
			// Each iteration must have consumed the moved element; otherwise it is dropped.
			key := affineValueKey{Root: movedElemSym}
			if st, ok := bodySnapshot.Affine[key]; ok && st.ConsumedBy == "" && (st.LiveProtocolType != nil || st.LiveProtocolDescription != "") {
				a.errorf(stmt.Pos(), "linear element %q moved out by `for ... in move` must be consumed in each iteration", movedElemSym.Name)
			}
			// The binding is loop-local — never let it leak into the outgoing state.
			delete(a.currentAffineValues, key)
		}
		// The drain consumes the whole container: discharge its aggregate must-consume obligation
		// (every element has now been moved out and individually consumed).
		if key, ok := a.lookupAffineValueKey(stmt.Source); ok {
			a.recordAffineConsumption(key, "drained by `for ... in move`")
		}
	}
}

// moveDrainBodyHasNonLocalExit reports whether a move-drain loop body contains a `break`,
// `continue`, or `return` that belongs to THIS loop (not a nested loop). Such an early exit
// would skip the remaining elements, leaking them un-consumed, so it is rejected.
func moveDrainBodyHasNonLocalExit(body []ast.Stmt) bool {
	for _, stmt := range body {
		if moveDrainStmtHasNonLocalExit(stmt) {
			return true
		}
	}
	return false
}

func moveDrainStmtHasNonLocalExit(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.BreakStmt, *ast.ContinueStmt, *ast.ReturnStmt:
		return true
	case *ast.IfStmt:
		if moveDrainBodyHasNonLocalExit(n.Then) || moveDrainBodyHasNonLocalExit(n.Else) {
			return true
		}
		for _, elif := range n.Elifs {
			if moveDrainBodyHasNonLocalExit(elif.Body) {
				return true
			}
		}
		return false
	case *ast.MatchStmt:
		for _, arm := range n.Arms {
			if moveDrainBodyHasNonLocalExit(arm.Body) {
				return true
			}
		}
		return false
	// Nested loops (for/while) introduce their own break/continue target, so a break/continue
	// inside them does not exit the drain — do not descend into their bodies for break/continue.
	// A `return` inside a nested loop still exits the function, but those nested constructs are
	// rare in a drain body; treat the conservative top-level scan as sufficient.
	default:
		return false
	}
}

func (a *Analyzer) analyzeLetDestructureStmt(stmt *ast.LetDestructureStmt) {
	if stmt == nil || stmt.Pattern == nil {
		return
	}
	valueType := a.analyzeValueExpr(stmt.Value, nil)
	fields, ok := a.resolveMoveBindStructPattern(stmt.Pattern, valueType)
	if !ok {
		return
	}
	for i, arg := range stmt.Pattern.Args {
		if i >= len(fields) {
			break
		}
		fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: stmt.Value, Field: fields[i].Name}
		a.recordAnalyzedExprType(fieldExpr, fields[i].Type)
		if arg.Name == "_" {
			a.consumeAffineValueExpr(fieldExpr, fields[i].Type, "discard destructured field")
			continue
		}
		sym := &Symbol{Name: arg.Name, Kind: SymbolLocal, Type: fields[i].Type, Node: stmt, Mutable: false}
		a.defineLocal(sym, arg.Position)
		a.recordValueBinding(sym, fieldExpr)
		a.recordFunctionValueBinding(sym, fieldExpr)
		a.recordImmutableSymbolOptimizationFacts(sym, fieldExpr)
		a.recordBorrowedOwnerRefBinding(sym, fieldExpr)
		a.recordRegionRefBinding(sym, fieldExpr)
	}
}

func (a *Analyzer) analyzeParallelForStmt(stmt *ast.ParallelForStmt) {
	sourceType := a.analyzeExpr(stmt.Source)
	// Band mode: a mutable Slice[T] source binds the loop variable to a disjoint Slice[T] band
	// (one per worker), so writes through the band are race-free by construction (the bands tile
	// the slice with no overlap). This is the first-class data-parallel mutable loop.
	bandElement, bandMode := sliceSourceElementType(sourceType)
	var itemType Type
	if bandMode {
		itemType = sourceType // the loop variable is a Slice[T] band
	} else {
		var ok bool
		itemType, ok = parallelForItemType(sourceType)
		if !ok {
			a.errorf(stmt.Source.Pos(), "parallel for requires a mutable Slice[T] (disjoint bands), a frozen packed store, or a readonly dense view, got %s", sourceType)
			itemType = invalidType
		} else if storeType, isStore := sourceType.(*PackedEnumStoreType); isStore {
			if !IsFrozenPackedEnumStoreType(storeType) {
				a.errorf(stmt.Source.Pos(), "parallel for requires a frozen packed store or readonly dense view, got %s", sourceType)
			}
		} else {
			facts, hasFacts := a.exprFacts[stmt.Source]
			if !hasFacts || !facts.ReadOnly || !facts.Contiguous || !facts.UnitStride || !facts.HasExactExtent() {
				a.errorf(stmt.Source.Pos(), "parallel for requires a readonly contiguous exact-extent view, got %s", sourceType)
			}
		}
	}
	// No enclosing `pool workers(w):` scope is required: `parallel for` falls back to the implicit
	// default pool (perf_cores() workers). An explicit pool scope still overrides the worker count.
	a.validateThreadTransferArg("parallel for", stmt.Source, sourceType)

	loopScope := NewScope(a.currentScope)
	loopSym := &Symbol{Name: stmt.Name, Kind: SymbolLocal, Type: itemType, Node: stmt, Mutable: false}
	savedScope := a.currentScope
	a.currentScope = loopScope
	a.defineLocal(loopSym, stmt.Pos())
	if stmt.IndexName != "" {
		indexSym := &Symbol{Name: stmt.IndexName, Kind: SymbolLocal, Type: a.namedTypes["usize"], Node: stmt, Mutable: false}
		a.defineLocal(indexSym, stmt.Pos())
	}
	a.currentScope = savedScope
	a.analyzeBlockWithAffineClone(stmt.Body, loopScope)

	rootLocals := []string{stmt.Name}
	if stmt.IndexName != "" {
		rootLocals = append(rootLocals, stmt.IndexName)
	}
	captureCollector := newParallelForCaptureCollector(a, a.currentScope, rootLocals...)
	captureCollector.collectStmts(stmt.Body)
	for _, name := range captureCollector.captureOrder {
		sym, ok := a.currentScope.Lookup(name)
		if !ok || !parallelForCapturableSymbolKind(sym.Kind) {
			continue
		}
		if !a.parallelForCaptureTypeAllowed(sym.Type, map[string]bool{}) {
			a.errorf(stmt.Pos(), "parallel for capture %q has unsupported shared type %s", name, sym.Type)
			continue
		}
		if bindingExpr, ok := a.currentValueBindings[sym]; ok && bindingExpr != nil {
			a.validateThreadTransferArg("parallel for", bindingExpr, sym.Type)
		} else {
			a.validateThreadTransferArg("parallel for", &ast.Ident{Position: stmt.Position, Name: name}, sym.Type)
		}
	}
	for _, msg := range captureCollector.errors {
		a.errorf(stmt.Pos(), "%s", msg)
	}
	bandDisjoint := false
	var disjointViews []string
	if bandMode {
		bandDisjoint, disjointViews = a.computeBandSourceDisjoint(stmt, captureCollector.captureOrder)
	}
	if a.parallelForInfo == nil {
		a.parallelForInfo = map[*ast.ParallelForStmt]*ParallelForInfo{}
	}
	a.parallelForInfo[stmt] = &ParallelForInfo{
		SourceType:           sourceType,
		ItemType:             itemType,
		Captures:             append([]string(nil), captureCollector.captureOrder...),
		BandMode:             bandMode,
		BandElement:          bandElement,
		BandSourceDisjoint:   bandDisjoint,
		DisjointViewCaptures: disjointViews,
	}
}

// computeBandSourceDisjoint proves (conservatively) that the writable band source buffer is
// disjoint from the buffers behind the loop's Slice/ReadView captures, so the backend may tag
// band writes `noalias` those captured reads. SOUND ONLY when every root resolves to a fresh
// LOCAL allocation: two distinct *parameters* resolve to distinct names yet can still alias at
// the caller, so a param (or param-derived) root is treated as un-provable and gives up the
// optimization. Returns the proof bit and the subset of capture names proven disjoint from the
// band (read-only views never conflict with each other, so only band⊥view is required).
func (a *Analyzer) computeBandSourceDisjoint(stmt *ast.ParallelForStmt, captures []string) (bool, []string) {
	bandRoots := a.aliasRootsForExpr(stmt.Source)
	if len(bandRoots) == 0 || !a.aliasRootsAllFreshLocal(bandRoots) {
		return false, nil
	}
	var disjoint []string
	for _, name := range captures {
		sym, ok := a.currentScope.Lookup(name)
		if !ok || sym == nil || !isSliceOrReadViewType(sym.Type) {
			continue
		}
		viewRoots := a.aliasRootsForExpr(&ast.Ident{Position: stmt.Position, Name: name})
		if len(viewRoots) == 0 || !a.aliasRootsAllFreshLocal(viewRoots) {
			continue
		}
		if aliasRootSetsOverlap(bandRoots, viewRoots) {
			continue
		}
		disjoint = append(disjoint, name)
	}
	if len(disjoint) == 0 {
		return false, nil
	}
	return true, disjoint
}

// aliasRootsAllFreshLocal reports whether every root resolves to a SymbolLocal (a fresh in-function
// allocation, never a parameter). Value-binding resolution in aliasRootsForExpr already collapses
// `b = a` / borrow laundering to the underlying root, so a root naming a param-backed local resolves
// to the param name (rejected here). Distinct fresh locals never share a buffer, so distinct
// fresh-local roots are provably disjoint.
func (a *Analyzer) aliasRootsAllFreshLocal(roots []string) bool {
	if a.currentScope == nil {
		return false
	}
	for _, root := range roots {
		base := root
		if dot := indexOfByte(base, '.'); dot >= 0 {
			base = base[:dot]
		}
		sym, ok := a.currentScope.Lookup(base)
		if !ok || sym == nil || sym.Kind != SymbolLocal {
			return false
		}
	}
	return true
}

func aliasRootSetsOverlap(a []string, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if aliasRootsOverlap(x, y) {
				return true
			}
		}
	}
	return false
}

func indexOfByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// isSliceOrReadViewType reports whether t is a Slice[T] or ReadView[T] (the data-parallel views
// whose backing buffer the band-disjointness proof reasons about).
func isSliceOrReadViewType(t Type) bool {
	gi, ok := StripAggregateStateType(t).(*GenericInstanceType)
	if !ok || gi == nil || len(gi.Args) != 1 {
		return false
	}
	return gi.Name == "Slice" || gi.Name == "ReadView"
}

// sliceSourceElementType reports whether t is a Slice[T] (the runtime data-parallel band view)
// and returns its element type T. Used to drive parallel-for band mode.
func sliceSourceElementType(t Type) (Type, bool) {
	if gi, ok := StripAggregateStateType(t).(*GenericInstanceType); ok && gi != nil && gi.Name == "Slice" && len(gi.Args) == 1 {
		return gi.Args[0], true
	}
	return nil, false
}

func parallelForItemType(t Type) (Type, bool) {
	switch tt := t.(type) {
	case *PackedEnumStoreType:
		if IsFrozenPackedEnumStoreType(tt) {
			return tt.Enum, true
		}
	case *ViewType:
		return tt.Elem, true
	case *GenericInstanceType:
		if itemType, ok := ChunksExactViewItemType(tt); ok {
			return itemType, true
		}
	case *SViewType, *DStrType:
		return builtinCharType(), true
	}
	return nil, false
}

func parallelForCapturableSymbolKind(kind SymbolKind) bool {
	switch kind {
	case SymbolParam, SymbolLocal, SymbolRegion, SymbolRegionMark:
		return true
	default:
		return false
	}
}

func (a *Analyzer) parallelForCaptureTypeAllowed(t Type, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	key := fmt.Sprintf("%T:%s", t, t.String())
	if seen[key] {
		return true
	}
	seen[key] = true
	switch tt := t.(type) {
	case *BuiltinType, *NullType, *NeverType, *TypeParamType, *FuncType, *ErrorSetType:
		return true
	case *ConstEnumType:
		return a.parallelForCaptureTypeAllowed(tt.Storage, seen)
	case *OptionalType:
		return a.parallelForCaptureTypeAllowed(tt.Value, seen)
	case *ErrorUnionType:
		return a.parallelForCaptureTypeAllowed(tt.Value, seen)
	case *ArrayType:
		return a.parallelForCaptureTypeAllowed(tt.Elem, seen)
	case *EnumType:
		if tt.Packed {
			return true
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if !a.parallelForCaptureTypeAllowed(payload, seen) {
					return false
				}
			}
		}
		for _, field := range tt.Common {
			if !a.parallelForCaptureTypeAllowed(field.Type, seen) {
				return false
			}
		}
		return true
	case *StructType:
		for _, field := range tt.Fields {
			if !a.parallelForCaptureTypeAllowed(field.Type, seen) {
				return false
			}
		}
		return true
	case *GenericInstanceType:
		base, ok := tt.Base.(*StructType)
		if !ok {
			return false
		}
		bindings := genericBindingsForStructInstance(base, tt.Args)
		regionBindings := regionBindingsForStructInstance(base, tt.Args)
		for _, field := range base.Fields {
			fieldType := field.Type
			if len(bindings) != 0 {
				fieldType = a.substituteType(fieldType, bindings, nil, regionBindings, nil)
			}
			if !a.parallelForCaptureTypeAllowed(fieldType, seen) {
				return false
			}
		}
		return true
	case *PackedEnumStoreType:
		return IsFrozenPackedEnumStoreType(tt)
	case *ViewType:
		return tt.SurfaceName == "packedview" && a.parallelForCaptureTypeAllowed(tt.Elem, seen)
	default:
		return false
	}
}
