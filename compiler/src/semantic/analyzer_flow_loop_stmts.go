package semantic

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) analyzeCanStmt(stmt *ast.CanStmt) {
	refs := a.resolvePermissionRefs(stmt.Permissions, true)
	if !stmt.SuppressPermissionInference {
		a.recordFunctionPermissionRefs(refs)
		a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
		return
	}
	granted := grantedPermissionRefs(refs)
	savedUsedPermissions := a.currentFunctionUsedPermissions
	savedUsedRefs := a.currentFunctionUsedPermissionRefs
	savedTrustedNonProgressDepth := a.currentTrustedNonProgressDepth
	savedTrustedAssumeProgressDepth := a.currentTrustedAssumeProgressDepth
	if permissionRefsContain(refs, "Unsafe", "NonProgress") {
		a.currentTrustedNonProgressDepth++
	}
	if permissionRefsContain(refs, "Unsafe", "AssumeProgress") {
		a.currentTrustedAssumeProgressDepth++
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
	a.recordFunctionPermissionRefs(remainingRefs)
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
	savedViewStaticLen := a.currentViewStaticLen
	a.currentViewStaticLen = cloneViewStaticLen(savedViewStaticLen)
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
	a.loopDepth++
	bodySnapshot := a.analyzeBlockWithAffineClone(stmt.Body, loopScope)
	a.loopDepth--
	a.currentIndexBounds = savedIndexBounds
	a.currentViewStaticLen = savedViewStaticLen
	if !blockDefinitelyExits(stmt.Body) {
		mergedAffine = mergeAffineValueStates(mergedAffine, bodySnapshot.Affine)
		mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, bodySnapshot.BorrowedOwnerRefs)
		mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, bodySnapshot.FunctionValues)
		mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, bodySnapshot.SpecializedValueTypes)
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
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
		if isStringArrayType(tt) {
			return iterLoopSourceInfo{ItemType: a.namedTypes["char"]}, true
		}
		return iterLoopSourceInfo{ItemType: tt.Elem, AllowRef: true, AllowMutableRef: true}, true
	case *DArrayType:
		return iterLoopSourceInfo{ItemType: tt.Elem, AllowRef: true, AllowMutableRef: !readOnly}, true
	case *ViewType:
		return iterLoopSourceInfo{ItemType: tt.Elem, AllowRef: true, AllowMutableRef: !readOnly}, true
	case *DArrayViewType:
		if tt.SurfaceName != "" && tt.SurfaceName != "dview" {
			return iterLoopSourceInfo{}, false
		}
		return iterLoopSourceInfo{ItemType: tt.Elem, AllowRef: true, AllowMutableRef: !readOnly}, true
	case *StoreRowsViewType:
		return iterLoopSourceInfo{ItemType: &StoreRowViewType{Store: tt.Store}}, true
	case *FrozenTreeRowsViewType:
		if tt.Category == nil {
			return iterLoopSourceInfo{ItemType: invalidType}, false
		}
		return iterLoopSourceInfo{ItemType: tt.Category}, true
	case *DStrType:
		return iterLoopSourceInfo{ItemType: a.namedTypes["char"]}, true
	case *SViewType:
		return iterLoopSourceInfo{ItemType: a.namedTypes["char"]}, true
	case *GenericInstanceType:
		if itemType, ok := EnumerateViewItemType(tt); ok {
			return iterLoopSourceInfo{ItemType: itemType}, true
		}
		if itemType, ok := FilteredViewItemType(tt); ok {
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
	if stmt.Mode == ast.IterBindValue && a.containsAffineHandleValues(info.ItemType, map[string]bool{}) {
		a.errorf(stmt.Pos(), "for value iteration does not support affine element type %s; use ref or mutable ref", info.ItemType)
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
	stmt.WhereFilter = a.rewriteFrozenTreeRowFieldFilterShorthand(loopScope, stmt.Pattern, sourceType, stmt.WhereFilter)
	stmt.Filter = a.rewriteFrozenTreeRowFieldFilterShorthand(loopScope, stmt.Pattern, sourceType, stmt.Filter)
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
	var bodySnapshot affineFlowSnapshot
	a.loopDepth++
	if stmt.Filter != nil {
		bodySnapshot = a.analyzeBlockWithConditionAffineClone(stmt.Body, loopScope, stmt.Filter, true)
	} else if stmt.WhereFilter != nil {
		bodySnapshot = a.analyzeBlockWithConditionAffineClone(stmt.Body, loopScope, stmt.WhereFilter, true)
	} else {
		bodySnapshot = a.analyzeBlockWithAffineClone(stmt.Body, loopScope)
	}
	a.loopDepth--
	if !blockDefinitelyExits(stmt.Body) {
		mergedAffine = mergeAffineValueStates(mergedAffine, bodySnapshot.Affine)
		mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, bodySnapshot.BorrowedOwnerRefs)
		mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, bodySnapshot.FunctionValues)
		mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, bodySnapshot.SpecializedValueTypes)
	}
	a.currentAffineValues = mergedAffine
	a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
	a.currentFunctionValues = mergedFunctionValues
	a.currentSpecializedValueTypes = mergedSpecializedValueTypes
}

func (a *Analyzer) rewriteFrozenTreeRowFieldFilterShorthand(scope *Scope, pattern ast.MoveBindPattern, sourceType Type, expr ast.Expr) ast.Expr {
	if expr == nil || scope == nil {
		return expr
	}
	namePattern, ok := pattern.(*ast.MoveBindNamePattern)
	if !ok || namePattern == nil || namePattern.Name == "_" {
		return expr
	}
	rowsType, ok := StripAggregateStateType(sourceType).(*FrozenTreeRowsViewType)
	if !ok || rowsType == nil || rowsType.Category == nil {
		return expr
	}
	var rewrite func(ast.Expr) ast.Expr
	rewrite = func(current ast.Expr) ast.Expr {
		switch n := current.(type) {
		case *ast.Ident:
			if n == nil || n.Name == namePattern.Name {
				return current
			}
			if _, exists := scope.Lookup(n.Name); exists {
				return current
			}
			if _, ok := TreeCategorySurfaceFieldInfo(rowsType.Category, n.Name); !ok {
				return current
			}
			return &ast.FieldExpr{
				Position: n.Position,
				Object:   &ast.Ident{Position: namePattern.Pos(), Name: namePattern.Name},
				Field:    n.Name,
			}
		case *ast.BinaryExpr:
			if n == nil {
				return current
			}
			n.Left = rewrite(n.Left)
			n.Right = rewrite(n.Right)
			return n
		case *ast.UnaryExpr:
			if n == nil {
				return current
			}
			n.Operand = rewrite(n.Operand)
			return n
		case *ast.ParenExpr:
			if n == nil {
				return current
			}
			n.Inner = rewrite(n.Inner)
			return n
		default:
			return current
		}
	}
	return rewrite(expr)
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
	itemType, ok := parallelForItemType(sourceType)
	if !ok {
		a.errorf(stmt.Source.Pos(), "parallel for requires a frozen packed store or readonly dense view, got %s", sourceType)
		itemType = invalidType
	}
	if ok {
		if storeType, isStore := sourceType.(*PackedEnumStoreType); isStore {
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
	if len(a.currentPoolScopes) == 0 {
		a.errorf(stmt.Pos(), "parallel for requires an enclosing pool scope")
	}
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
	if a.parallelForInfo == nil {
		a.parallelForInfo = map[*ast.ParallelForStmt]*ParallelForInfo{}
	}
	a.parallelForInfo[stmt] = &ParallelForInfo{
		SourceType: sourceType,
		ItemType:   itemType,
		Captures:   append([]string(nil), captureCollector.captureOrder...),
	}
}

func parallelForItemType(t Type) (Type, bool) {
	switch tt := t.(type) {
	case *PackedEnumStoreType:
		if IsFrozenPackedEnumStoreType(tt) {
			return tt.Enum, true
		}
	case *DArrayViewType:
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
	case *DArrayViewType:
		return tt.SurfaceName == "packedview" && a.parallelForCaptureTypeAllowed(tt.Elem, seen)
	default:
		return false
	}
}
