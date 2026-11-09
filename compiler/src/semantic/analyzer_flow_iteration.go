package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
)

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
	facts, hasFacts := a.lookupExprFacts(sourceExpr)
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
	case *DictType:
		return iterLoopSourceInfo{ItemType: DictEntryTupleType(tt)}, true
	case *ViewType:
		if tt.SurfaceName != "" && tt.SurfaceName != "view" {
			return iterLoopSourceInfo{}, false
		}
		// A read-only `view[T]` binds by value or immutable ref only; a `mutable view[T]` additionally
		// permits mutable element refs (write-through). `readOnly` facts can further downgrade a
		// mutable view (e.g. wrapped in `readonly(...)`).
		return iterLoopSourceInfo{ItemType: tt.Elem, AllowRef: true, AllowMutableRef: tt.Mutable && !readOnly}, true
	case *StoreRowsViewType:
		return iterLoopSourceInfo{ItemType: &StoreRowViewType{Store: tt.Store}}, true
	case *CStrType:
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
		if !IsInvalidType(sourceType) {
			a.errorf(stmt.Source.Pos(), "iterable for loop currently requires an array, dynamic array, view, store.rows(), frozen tree row view, string-like iterable, ChunksExactView, source.enumerate(), children(node), or a projected tree attribute sequence, got %s", sourceType)
		}
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
		// The drain moves every element out of the source: a reference or view taken into it
		// before the loop is stale from here on, in the body and after it.
		a.invalidateStorageViewsForMovedContainer(stmt.Source, sourceType)
	}
	if stmt.Mode == ast.IterBindValue && !stmt.MovedSource && a.containsAffineHandleValues(info.ItemType, map[string]bool{}) {
		// AUTO-BORROW: read-only iteration over affine elements binds by reference —
		// a per-iteration value copy would duplicate linear handles, and the removed
		// `for ref x` spelling made authors say so manually. Copy-vs-ref on an
		// immutable binding is semantically invisible, so upgrade the mode here when
		// a borrow is legal; only the genuinely un-borrowable cases still error.
		if info.AllowRef && isBorrowableAffineOwnerType(info.ItemType) {
			stmt.Mode = ast.IterBindRef
		} else {
			a.errorf(stmt.Pos(), "iteration over affine element type %s cannot copy or borrow the elements; use `for x in move c` to drain and consume each element", info.ItemType)
		}
	}
	if stmt.Mode != ast.IterBindValue && a.containsAffineHandleValues(info.ItemType, map[string]bool{}) && !isBorrowableAffineOwnerType(info.ItemType) {
		a.errorf(stmt.Pos(), "references to values containing linear handles are not supported; got %s&", info.ItemType)
	}
	switch stmt.Mode {
	case ast.IterBindRef:
		if !info.AllowRef {
			a.errorf(stmt.Pos(), "by-reference iteration requires an addressable array-like iterable, got %s", sourceType)
		}
	case ast.IterBindMutableRef:
		if !info.AllowMutableRef {
			a.errorf(stmt.Pos(), "`for mutable` requires a writable addressable array-like iterable, got %s", sourceType)
		}
	}

	loopScope := NewScope(a.currentScope)
	a.bindIterLoopPattern(loopScope, stmt.Pattern, stmt.Mode, info.ItemType, info.ItemFacts, info.HasItemFacts)
	// The return-borrow flow walk reads a loop binding's borrows from the iterated source: an
	// element (or a field destructured from it) holds only what the container holds.
	if a.iterBindingSources == nil {
		a.iterBindingSources = map[*Symbol]ast.Expr{}
	}
	// A by-value binder over an eligible tracked local container takes the container's element
	// provenance (ownedExtractionRegion reads it instead of the container's own region).
	binderElementState, binderTracked := regionRefState{}, false
	if a.currentElementBinderLoops[stmt] && stmt.Mode == ast.IterBindValue && a.currentScope != nil {
		if ident, ok := stmt.Source.(*ast.Ident); ok {
			if sourceSym, ok := a.currentScope.Lookup(ident.Name); ok {
				binderElementState, binderTracked = a.currentElementStates[sourceSym]
			}
		}
	}
	for _, sym := range loopScope.Symbols {
		a.iterBindingSources[sym] = stmt.Source
		if binderTracked {
			a.setIterBinderElementState(sym, cloneRegionRefState(binderElementState))
		}
		if stmt.Mode != ast.IterBindValue {
			if a.iterBindingByRef == nil {
				a.iterBindingByRef = map[*Symbol]bool{}
			}
			a.iterBindingByRef[sym] = true
		}
	}
	// A by-value binder is an element copy: a view element keeps pointing wherever the
	// container's elements point, so the binder carries the element provenance an index
	// read would (`for c in caps: dst.push(c)` must be checked like `dst.push(caps[i])`).
	if stmt.Mode == ast.IterBindValue && !stmt.MovedSource {
		if state, ok := a.iterBinderRegionRefState(stmt.Source, sourceType, info.ItemType, binderElementState, binderTracked); ok {
			for _, sym := range loopScope.Symbols {
				a.recordResolvedRegionRefBinding(sym, state)
			}
		}
	}
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
	// The filters run again on every iteration: their view uses belong to the back-edge check.
	a.pushStorageViewLoopUseFrame(a.currentScope, stmt.Body)
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
	entryAffine := a.cloneAffineValueStates()
	outerScope := a.currentScope
	a.havocLoopWrittenFacts(stmt.Body, nil)
	a.pushLoopAffineFrame()
	a.loopDepth++
	if stmt.Filter != nil {
		bodySnapshot = a.analyzeLoopBodyKeepingWrites(func() affineFlowSnapshot {
			return a.analyzeBlockWithConditionAffineClone(stmt.Body, loopScope, stmt.Filter, true)
		})
	} else if stmt.WhereFilter != nil {
		bodySnapshot = a.analyzeLoopBodyKeepingWrites(func() affineFlowSnapshot {
			return a.analyzeBlockWithConditionAffineClone(stmt.Body, loopScope, stmt.WhereFilter, true)
		})
	} else {
		bodySnapshot = a.analyzeBlockWithAffineClone(stmt.Body, loopScope)
	}
	a.loopDepth--
	a.checkStorageViewLoopBackEdge(bodySnapshot.StorageViewDeps, blockDefinitelyExits(stmt.Body))
	mergedFunctionValues = a.mergeLoopJumpFunctionValues(mergedFunctionValues)
	mergedSpecializedValueTypes = a.mergeLoopJumpSpecializedTypes(mergedSpecializedValueTypes)
	continuedAffine := a.finishLoopAffineFrame(entryAffine, bodySnapshot.Affine, blockDefinitelyExits(stmt.Body), outerScope, stmt.Pos())
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
	mergedAffine = mergeAffineValueStates(mergedAffine, continuedAffine)
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
