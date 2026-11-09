package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

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
			facts, hasFacts := a.lookupExprFacts(stmt.Source)
			if !hasFacts || !facts.ReadOnly || !facts.Contiguous || !facts.UnitStride || !facts.HasExactExtent() {
				a.errorf(stmt.Source.Pos(), "parallel for requires a readonly contiguous exact-extent view, got %s", sourceType)
			}
		}
	}
	// No enclosing `pool workers(w):` scope is required: `parallel for` falls back to the implicit
	// default pool (perf_cores() workers). An explicit pool scope still overrides the worker count.
	a.validateThreadTransferArg("parallel for", stmt.Source, sourceType, nil)

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
			a.validateThreadTransferArg("parallel for", bindingExpr, sym.Type, nil)
		} else {
			a.validateThreadTransferArg("parallel for", &ast.Ident{Position: stmt.Position, Name: name}, sym.Type, nil)
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
	case *SViewType, *CStrType:
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
			if len(bindings) != 0 || len(regionBindings) != 0 {
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

// iterBinderRegionRefState is the region provenance of one by-value element of source: the
// tracked element provenance of an eligible local container, else the merge of the source's
// per-index states, bounded by the container's region when the element owns arena storage.
func (a *Analyzer) iterBinderRegionRefState(source ast.Expr, sourceType Type, itemType Type, tracked regionRefState, isTracked bool) (regionRefState, bool) {
	if itemType == nil || !a.typeCanContainRegionRefs(itemType, map[string]bool{}) {
		return regionRefState{}, false
	}
	if isTracked {
		if !hasRegionProvenance(tracked) {
			return regionRefState{}, false
		}
		return cloneRegionRefState(tracked), true
	}
	state, ok := regionRefState{}, false
	if srcState, srcOK := a.regionRefStateForExpr(source); srcOK && hasRegionProvenance(srcState) {
		state, ok = summarizeRegionIndexStates(srcState)
	}
	if a.typeMayOwnArenaStorage(itemType, map[string]bool{}) {
		if owner, ownerOK := a.containerRegionDependency(sourceType); ownerOK {
			if !ok {
				return owner, true
			}
			if merged, mergedOK := mergeRegionRefStates(state, owner); mergedOK {
				return merged, true
			}
		}
	}
	return state, ok
}
