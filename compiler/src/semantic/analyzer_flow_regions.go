package semantic

import (
	"fmt"
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) analyzeLockStmt(stmt *ast.LockStmt) {
	lockCall := &ast.CallExpr{
		Position: stmt.Position,
		Func:     &ast.Ident{Position: stmt.Position, Name: "mutex_lock"},
		Args: []ast.Expr{&ast.CastExpr{
			Position: stmt.Mutex.Pos(),
			Operand: &ast.AddrOfExpr{
				Position: stmt.Mutex.Pos(),
				Operand:  stmt.Mutex,
			},
			Target: &ast.RefType{
				Position: stmt.Mutex.Pos(),
				Elem:     &ast.NamedType{Position: stmt.Mutex.Pos(), Name: "Mutex"},
				State:    ast.RefStateNonNull,
				Storage:  ast.RefStorageAny,
				Explicit: true,
			},
		}},
	}
	guardType := a.analyzeExpr(lockCall)
	savedScope := a.currentScope
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedCheckpoints := a.currentCheckpoints
	savedRegionRefs := a.currentRegionRefs
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedTreeOwner := a.currentTreeAllocOwner
	savedAllocExpr := a.currentAllocExpr
	guardSym := &Symbol{Name: stmt.GuardName, Kind: SymbolLocal, Type: guardType, Node: stmt, Mutable: true}
	a.currentScope = NewScope(savedScope)
	a.currentRegions = a.cloneRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentCheckpoints = a.cloneCheckpointStates()
	a.currentRegionRefs = a.cloneRegionRefStates()
	a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	a.defineLocal(guardSym, stmt.Pos())
	for _, inner := range stmt.Body {
		a.analyzeStmt(inner)
	}
	if a.currentAffineValues != nil {
		for key := range a.currentAffineValues {
			if key.Root == guardSym {
				delete(a.currentAffineValues, key)
			}
		}
	}
	a.currentScope = savedScope
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentCheckpoints = savedCheckpoints
	a.currentRegionRefs = savedRegionRefs
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
	a.currentTreeAllocOwner = savedTreeOwner
	a.currentAllocExpr = savedAllocExpr
}

func scopedArenaOwnerRefType(pos lexer.Pos) ast.TypeExpr {
	return &ast.MutableType{Position: pos, Elem: &ast.RefType{
		Position: pos,
		Elem:     &ast.NamedType{Position: pos, Name: "Arena"},
		State:    ast.RefStateNonNull,
		Storage:  ast.RefStorageAny,
		Explicit: true,
	}}
}

func scopedArenaOwnerDecl(pos lexer.Pos, regionName string, ownerName string) *ast.VarDeclStmt {
	ownerType := scopedArenaOwnerRefType(pos)
	return &ast.VarDeclStmt{
		Position: pos,
		Name:     ownerName,
		Type:     ownerType,
		Value: &ast.CastExpr{
			Position: pos,
			Operand:  &ast.AddrOfExpr{Position: pos, Operand: &ast.Ident{Position: pos, Name: regionName}},
			Target:   ownerType,
		},
	}
}

func scopedArenaInStoreStmt(stmt *ast.RegionStmt) *ast.InStoreStmt {
	body := make([]ast.Stmt, 0, len(stmt.Body)+1)
	if stmt.OwnerName != "" {
		body = append(body, scopedArenaOwnerDecl(stmt.Position, stmt.Name, stmt.OwnerName))
	}
	body = append(body, stmt.Body...)
	return &ast.InStoreStmt{
		Position: stmt.Position,
		Store:    &ast.Ident{Position: stmt.Position, Name: stmt.Name},
		Body:     body,
	}
}

func (a *Analyzer) analyzeScopedArenaStmt(stmt *ast.RegionStmt) {
	savedScope := a.currentScope
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedCheckpoints := a.currentCheckpoints
	savedRegionRefs := a.currentRegionRefs
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	a.currentScope = NewScope(savedScope)
	a.currentRegions = a.cloneRegionStates()
	a.currentRegionMarks = a.cloneRegionMarkStates()
	a.currentCheckpoints = a.cloneCheckpointStates()
	a.currentRegionRefs = a.cloneRegionRefStates()
	a.currentPackedVariantViews = a.clonePackedVariantViewBindings()
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	sym := a.analyzeRegionDecl(stmt)
	a.analyzeInStoreStmt(scopedArenaInStoreStmt(stmt))
	// Scoped region: the owner is discharged automatically at block exit, so it
	// satisfies the must-consume obligation registered in analyzeRegionDecl —
	// unless the body already consumed it (an explicit `destroy`, or
	// `adopt ... into <outer>`), in which case discharging again would be a
	// double-consume.
	if sym != nil && !a.currentRegions[sym].Destroyed {
		a.recordAffineConsumption(affineValueKey{Root: sym}, "region scope exit")
	}
	a.currentScope = savedScope
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentCheckpoints = savedCheckpoints
	a.currentRegionRefs = savedRegionRefs
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
}

// returnedRegionOwner reports the region owner symbol of a `return move <region>`
// expression (approach A: ownership transfer is inferred from an explicit move
// of a live region owner).
// isOwnedTypeExpr reports whether a declared type carries the `owned`
// ownership qualifier (e.g. `owned Arena`, `mutable owned Arena`).
func isOwnedTypeExpr(t ast.TypeExpr) bool {
	switch n := t.(type) {
	case *ast.OwnedType:
		return true
	case *ast.MutableType:
		return isOwnedTypeExpr(n.Elem)
	default:
		return false
	}
}

// registerOwnedStoreOwner makes a binding/parameter declared `owned <store>` a
// first-class region owner: it joins currentRegions (so destroy/leak/move and
// new[owner] work) and inherits the affine must-consume obligation. A plain
// (non-owned) store value or `&` borrow is left exactly as it is today.
// symbolIsRegionOwner reports whether a symbol has been registered as a region
// owner (region decl, `owned <store>` binding/param, or moved-in owned region),
// regardless of its SymbolKind.
func (a *Analyzer) symbolIsRegionOwner(sym *Symbol) bool {
	if sym == nil || a.currentRegions == nil {
		return false
	}
	_, ok := a.currentRegions[sym]
	return ok
}

func (a *Analyzer) registerOwnedStoreOwner(sym *Symbol) {
	if sym == nil {
		return
	}
	if a.currentRegions == nil {
		a.currentRegions = map[*Symbol]regionState{}
	}
	a.currentRegions[sym] = regionState{}
	a.markLiveProtocolDescription(affineValueKey{Root: sym}, "owned region")
}

func (a *Analyzer) returnedRegionOwner(expr ast.Expr) (*Symbol, bool) {
	moved, ok := explicitMoveOperand(expr)
	if !ok {
		return nil, false
	}
	ident, ok := moved.(*ast.Ident)
	if !ok {
		return nil, false
	}
	sym, state := a.lookupRegionState(ident.Name)
	if sym == nil || state.Destroyed {
		return nil, false
	}
	return sym, true
}

// transferRegionOwnerOut consumes a region owner that has left the caller's
// control (moved into a worker thread): the caller can no longer destroy, reset,
// allocate from, or read through it, and dependent refs are invalidated — but it
// is not freed (the new owner is responsible for that).
func (a *Analyzer) transferRegionOwnerOut(sym *Symbol, reason string) {
	if sym == nil {
		return
	}
	if state, ok := a.currentRegions[sym]; ok {
		state.Destroyed = true
		a.currentRegions[sym] = state
		a.invalidateRegionRefs(sym, func(regionDependencyState) bool { return true }, reason)
		a.invalidateRegionMarks(sym, func(regionMarkState) bool { return true }, reason)
	}
	a.recordAffineConsumption(affineValueKey{Root: sym}, reason)
}

// regionOwnerIdent reports a live region owner referenced as a bare ident (NOT
// moved). Used to require an explicit `move` when transferring ownership.
func (a *Analyzer) regionOwnerIdent(expr ast.Expr) (*Symbol, bool) {
	if _, isMove := explicitMoveOperand(expr); isMove {
		return nil, false
	}
	inner := expr
	if paren, ok := inner.(*ast.ParenExpr); ok {
		inner = paren.Inner
	}
	ident, ok := inner.(*ast.Ident)
	if !ok {
		return nil, false
	}
	sym, state := a.lookupRegionState(ident.Name)
	if sym == nil || state.Destroyed {
		return nil, false
	}
	return sym, true
}

func unwrapToCallExpr(expr ast.Expr) *ast.CallExpr {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return unwrapToCallExpr(n.Inner)
	case *ast.MoveExpr:
		return unwrapToCallExpr(n.Operand)
	case *ast.CallExpr:
		return n
	default:
		return nil
	}
}

// markReceivedOwnedRegion registers a binding whose initializer is a call to a
// function returning an owned region (FuncType.ReturnsOwnedRegion) as a
// first-class region owner: it joins currentRegions (so destroy/leak/checkpoint
// and new[binding] work) and inherits the affine must-consume obligation.
func (a *Analyzer) markReceivedOwnedRegion(sym *Symbol, value ast.Expr) {
	if sym == nil || value == nil {
		return
	}
	call := unwrapToCallExpr(value)
	if call == nil {
		return
	}
	fnType, _ := a.exprTypes[call.Func].(*FuncType)
	if fnType == nil || !fnType.ReturnsOwnedRegion {
		return
	}
	if a.currentRegions == nil {
		a.currentRegions = map[*Symbol]regionState{}
	}
	a.currentRegions[sym] = regionState{}
	a.markLiveProtocolDescription(affineValueKey{Root: sym}, "region owner")
}

func (a *Analyzer) analyzeRegionDecl(stmt *ast.RegionStmt) *Symbol {
	if stmt.Capacity != nil {
		capacityType := a.analyzeExpr(stmt.Capacity)
		if !IsNumericType(capacityType) {
			a.errorf(stmt.Capacity.Pos(), "region capacity must be numeric, got %s", capacityType)
		}
	}
	// Only the libc-malloc backing override is currently supported; an empty allocator
	// keeps the compile-time default backend.
	if stmt.Allocator != "" && stmt.Allocator != "malloc" {
		a.errorf(stmt.Pos(), "unknown region allocator %q (expected \"malloc\")", stmt.Allocator)
	}
	arenaType, ok := a.namedTypes["Arena"]
	if !ok {
		a.errorf(stmt.Pos(), "missing builtin Arena type for region lowering")
		arenaType = invalidType
	}
	sym := &Symbol{Name: stmt.Name, Kind: SymbolRegion, Type: arenaType, Node: stmt, Mutable: false}
	a.defineLocal(sym, stmt.Pos())
	if a.currentRegions != nil {
		a.currentRegions[sym] = regionState{}
	}
	// Record this region's lifetime ordinal (declaration order == nesting order
	// for live regions). Outer regions get lower ordinals and thus outlive inner
	// ones. See regionLifetimeOrdinals.
	if a.regionLifetimeOrdinals == nil {
		a.regionLifetimeOrdinals = map[string]int{}
	}
	a.regionLifetimeCounter++
	a.regionLifetimeOrdinals[stmt.Name] = a.regionLifetimeCounter
	// A region owns a bulk allocation, so it is an affine resource that must be
	// consumed on every path before scope exit (like any other linear value).
	// A bare `region NAME(...)` must be matched by `destroy NAME`; a scoped
	// `region NAME(...):` block discharges this automatically at block exit
	// (see analyzeScopedArenaStmt). This is what makes a forgotten region a
	// compile error rather than a silent leak.
	a.markLiveProtocolDescription(affineValueKey{Root: sym}, "region owner")
	a.currentTreeAllocOwner = treeAllocOwnerBinding{Kind: treeAllocOwnerRegion, RegionName: stmt.Name}
	a.currentAllocExpr = &ast.Ident{Position: stmt.Position, Name: stmt.Name}
	return sym
}

func (a *Analyzer) analyzeInStoreStmt(stmt *ast.InStoreStmt) {
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedPackedVariantViews := a.currentPackedVariantViews
	savedTreeAllocOwner := a.currentTreeAllocOwner
	savedAllocExpr := a.currentAllocExpr
	if owner, _, ok := a.classifyTreeAllocOwnerExpr(stmt.Store); ok {
		a.currentTreeAllocOwner = owner
		if owner.Kind == treeAllocOwnerArena || owner.Kind == treeAllocOwnerRegion {
			a.currentAllocExpr = stmt.Store
		} else {
			a.currentAllocExpr = nil
		}
		a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
		a.currentTreeAllocOwner = savedTreeAllocOwner
		a.currentAllocExpr = savedAllocExpr
		a.currentPackedVariantViews = savedPackedVariantViews
		a.currentPackedStores = savedPackedStores
		a.currentPackedStoreResolutions = savedPackedStoreResolutions
		return
	}
	storeType := a.exprTypes[stmt.Store]
	if storeType == nil {
		storeType = a.analyzeExpr(stmt.Store)
	}
	if treeStore, ok := storeType.(*TreeStoreType); ok {
		a.currentTreeAllocOwner = treeAllocOwnerBinding{Kind: treeAllocOwnerStore, StoreFamily: treeStore.Family}
		a.currentAllocExpr = nil
		a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
		a.currentTreeAllocOwner = savedTreeAllocOwner
		a.currentAllocExpr = savedAllocExpr
		a.currentPackedVariantViews = savedPackedVariantViews
		a.currentPackedStores = savedPackedStores
		a.currentPackedStoreResolutions = savedPackedStoreResolutions
		return
	}
	packedStore, ok := storeType.(*PackedEnumStoreType)
	if !ok {
		a.errorf(stmt.Store.Pos(), "in-block requires a tree store, packed enum store, perm, an Arena value, or an Arena reference, got %s", storeType)
		a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
		a.currentTreeAllocOwner = savedTreeAllocOwner
		a.currentAllocExpr = savedAllocExpr
		a.currentPackedVariantViews = savedPackedVariantViews
		a.currentPackedStores = savedPackedStores
		a.currentPackedStoreResolutions = savedPackedStoreResolutions
		return
	}
	a.currentPackedStores = a.clonePackedStores()
	a.currentPackedStoreResolutions = a.clonePackedStoreResolutions()
	if a.currentPackedStores == nil {
		a.currentPackedStores = map[string]*PackedEnumStoreType{}
	}
	a.currentPackedStores[packedStore.Enum.Name] = packedStore
	a.analyzeBlockWithRegionClone(stmt.Body, NewScope(a.currentScope))
	a.currentTreeAllocOwner = savedTreeAllocOwner
	a.currentAllocExpr = savedAllocExpr
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
}

func (a *Analyzer) analyzeMarkStmt(stmt *ast.MarkStmt) {
	regionSym, state := a.lookupRegionState(stmt.RegionName)
	if regionSym == nil {
		a.errorf(stmt.Pos(), "undefined region %q", stmt.RegionName)
		return
	}
	if state.Destroyed {
		a.errorf(stmt.Pos(), "region %q has already been destroyed", stmt.RegionName)
		return
	}
	markType, ok := a.namedTypes["ArenaMark"]
	if !ok {
		a.errorf(stmt.Pos(), "missing builtin ArenaMark type for region checkpoints")
		markType = invalidType
	}
	markSym := &Symbol{Name: stmt.Name, Kind: SymbolRegionMark, Type: markType, Node: stmt, Mutable: false}
	a.defineLocal(markSym, stmt.Pos())
	state.Generation++
	a.currentRegions[regionSym] = state
	if a.currentRegionMarks == nil {
		a.currentRegionMarks = map[*Symbol]regionMarkState{}
	}
	a.currentRegionMarks[markSym] = regionMarkState{Region: regionSym, Generation: state.Generation, Valid: true}
}

func (a *Analyzer) restoreFromRegionCheckpoint(pos lexer.Pos, regionSym *Symbol, markSym *Symbol, markState regionMarkState) {
	state, ok := a.currentRegions[regionSym]
	if !ok {
		return
	}
	if !markState.Valid {
		a.errorf(pos, "checkpoint %q is invalid after %s", markSym.Name, markState.InvalidatedBy)
		return
	}
	before := state.Generation
	reason := fmt.Sprintf("restore of region %q from checkpoint %q", regionSym.Name, markSym.Name)
	a.invalidateRegionRefs(regionSym, func(dep regionDependencyState) bool {
		return dep.Generation >= markState.Generation
	}, reason)
	a.invalidateRegionMarks(regionSym, func(other regionMarkState) bool {
		return other.Generation > markState.Generation
	}, reason)
	state.Generation = markState.Generation
	a.currentRegions[regionSym] = state
	a.recordRegionInvalidateTransform(pos, regionSym.Name, markSym.Name, "restore region checkpoint", before, markState.Generation)
	if saved, ok := a.currentRegionMarks[markSym]; ok {
		saved.Valid = true
		saved.InvalidatedBy = ""
		a.currentRegionMarks[markSym] = saved
	}
}

func (a *Analyzer) analyzeCheckpointStmt(stmt *ast.CheckpointStmt) {
	if stmt == nil {
		return
	}
	scope := a.currentScope
	if len(stmt.Body) != 0 {
		scope = NewScope(a.currentScope)
	}
	binding, ok := a.bindCheckpointTarget(stmt.Pos(), stmt.Name, stmt.Target, scope, stmt)
	if !ok {
		if len(stmt.Body) != 0 {
			a.analyzeBlockWithAffineClone(stmt.Body, NewScope(a.currentScope))
		}
		return
	}
	if len(stmt.Body) != 0 {
		a.analyzeBlockWithAffineClone(stmt.Body, scope)
		if binding.regionSym != nil {
			a.restoreFromRegionCheckpoint(stmt.Pos(), binding.regionSym, binding.markSym, binding.regionMarkState)
		}
	}
}

type checkpointBindingHandle struct {
	markSym         *Symbol
	regionSym       *Symbol
	regionMarkState regionMarkState
}

func (a *Analyzer) bindCheckpointTarget(pos lexer.Pos, name string, target ast.Expr, scope *Scope, node ast.Node) (checkpointBindingHandle, bool) {
	if target == nil {
		return checkpointBindingHandle{}, false
	}
	targetType := a.analyzeExpr(target)
	if ident, ok := target.(*ast.Ident); ok {
		if regionSym, state := a.lookupRegionState(ident.Name); regionSym != nil {
			if state.Destroyed {
				a.errorf(pos, "region %q has already been destroyed", ident.Name)
				return checkpointBindingHandle{}, false
			}
			markType, ok := a.namedTypes["ArenaMark"]
			if !ok {
				a.errorf(pos, "missing builtin ArenaMark type for region checkpoints")
				markType = invalidType
			}
			markSym := &Symbol{Name: name, Kind: SymbolRegionMark, Type: markType, Node: node, Mutable: false}
			if scope != nil {
				a.defineLocalInScope(scope, markSym, pos)
			}
			state.Generation++
			a.currentRegions[regionSym] = state
			if a.currentRegionMarks == nil {
				a.currentRegionMarks = map[*Symbol]regionMarkState{}
			}
			markState := regionMarkState{Region: regionSym, Generation: state.Generation, Valid: true}
			a.currentRegionMarks[markSym] = markState
			return checkpointBindingHandle{markSym: markSym, regionSym: regionSym, regionMarkState: markState}, true
		}
	}
	if _, ok := checkpointDArraySourceType(targetType); !ok {
		a.errorf(target.Pos(), "checkpoint requires a region or mutable darray value, got %s", targetType)
		return checkpointBindingHandle{}, false
	}
	markSym := &Symbol{Name: name, Kind: SymbolCheckpoint, Type: a.namedTypes["usize"], Node: node, Mutable: false}
	if scope != nil {
		a.defineLocalInScope(scope, markSym, pos)
	}
	if a.currentCheckpoints == nil {
		a.currentCheckpoints = map[*Symbol]checkpointState{}
	}
	a.currentCheckpoints[markSym] = checkpointState{Kind: checkpointKindDArray, Target: target, TargetType: targetType, Valid: true}
	return checkpointBindingHandle{markSym: markSym}, true
}

func anonymousCheckpointName(pos lexer.Pos, index int) string {
	return fmt.Sprintf("__anon_checkpoint_%d_%d_%d", pos.Line, pos.Col, index)
}

func (a *Analyzer) analyzeGroupedCheckpointStmt(stmt *ast.GroupedCheckpointStmt) {
	if stmt == nil {
		return
	}
	regionRestores := make([]checkpointBindingHandle, 0, len(stmt.Targets))
	for i, target := range stmt.Targets {
		binding, ok := a.bindCheckpointTarget(stmt.Pos(), anonymousCheckpointName(stmt.Pos(), i), target, nil, stmt)
		if !ok {
			continue
		}
		if binding.regionSym != nil {
			regionRestores = append(regionRestores, binding)
		}
	}
	a.analyzeBlockWithAffineClone(stmt.Body, NewScope(a.currentScope))
	for i := len(regionRestores) - 1; i >= 0; i-- {
		binding := regionRestores[i]
		a.restoreFromRegionCheckpoint(stmt.Pos(), binding.regionSym, binding.markSym, binding.regionMarkState)
	}
}

func (a *Analyzer) analyzeRestoreStmt(stmt *ast.RestoreStmt) {
	regionSym, state := a.lookupRegionState(stmt.RegionName)
	if regionSym == nil {
		a.errorf(stmt.Pos(), "undefined region %q", stmt.RegionName)
		return
	}
	if state.Destroyed {
		a.errorf(stmt.Pos(), "region %q has already been destroyed", stmt.RegionName)
		return
	}
	markSym, markState := a.lookupRegionMark(stmt.MarkName)
	if markSym == nil {
		a.errorf(stmt.Pos(), "undefined checkpoint %q", stmt.MarkName)
		return
	}
	if markState.Region != regionSym {
		a.errorf(stmt.Pos(), "checkpoint %q belongs to region %q, not %q", stmt.MarkName, markState.Region.Name, stmt.RegionName)
		return
	}
	if !markState.Valid {
		a.errorf(stmt.Pos(), "checkpoint %q is invalid after %s", stmt.MarkName, markState.InvalidatedBy)
		return
	}
	a.restoreFromRegionCheckpoint(stmt.Pos(), regionSym, markSym, markState)
}

func (a *Analyzer) analyzeRestoreCheckpointStmt(stmt *ast.RestoreCheckpointStmt) {
	if stmt == nil {
		return
	}
	if markSym, markState := a.lookupRegionMark(stmt.Name); markSym != nil {
		a.restoreFromRegionCheckpoint(stmt.Pos(), markState.Region, markSym, markState)
		return
	}
	markSym, state := a.lookupCheckpoint(stmt.Name)
	if markSym == nil {
		a.errorf(stmt.Pos(), "undefined checkpoint %q", stmt.Name)
		return
	}
	if !state.Valid {
		a.errorf(stmt.Pos(), "checkpoint %q is invalid after %s", stmt.Name, state.InvalidatedBy)
	}
}

func (a *Analyzer) analyzeResetStmt(stmt *ast.ResetStmt) {
	regionSym, state := a.lookupRegionState(stmt.Name)
	if regionSym == nil {
		a.errorf(stmt.Pos(), "undefined region %q", stmt.Name)
		return
	}
	if state.Destroyed {
		a.errorf(stmt.Pos(), "region %q has already been destroyed", stmt.Name)
		return
	}
	reason := fmt.Sprintf("reset of region %q", stmt.Name)
	before := state.Generation
	a.invalidateRegionRefs(regionSym, func(regionDependencyState) bool { return true }, reason)
	a.invalidateRegionMarks(regionSym, func(regionMarkState) bool { return true }, reason)
	state.Generation = 0
	a.currentRegions[regionSym] = state
	a.recordRegionInvalidateTransform(stmt.Pos(), stmt.Name, "", "reset region", before, 0)
}

func (a *Analyzer) recordRegionInvalidateTransform(pos lexer.Pos, regionName string, checkpointName string, operation string, before int, after int) {
	if a == nil || regionName == "" {
		return
	}
	details := []FactTransformDetail{{Name: "operation", Value: operation}}
	if checkpointName != "" {
		details = append(details, FactTransformDetail{Name: "checkpoint", Value: checkpointName})
	}
	details = append(details, FactTransformDetail{Name: "generation_before", Value: strconv.Itoa(before)})
	afterText := strconv.Itoa(after)
	if after < 0 {
		afterText = "destroyed"
	}
	details = append(details, FactTransformDetail{Name: "generation_after", Value: afterText})
	a.currentRegionFactTransforms = append(a.currentRegionFactTransforms, FactTransform{
		Kind:       FactTransformInvalidate,
		Classes:    []FactClass{FactRegionDeps},
		Target:     regionName,
		Source:     checkpointName,
		SourcePos:  pos,
		SourceKind: FactSourceRegion,
		Details:    details,
		Reason:     operation,
	})
}
