package semantic

import (
	"elisacore/src/ast"
	"strconv"
	"strings"
)

// conditionVariantPatternBase is the enum a variant `is` pattern destructures against.
// Inside a packed enum's match arm the scrutinee is narrowed to that arm's variant view
// (packedview[E.Pair]); a nested `e is E.Two(x, _)` still names E's variants, so its
// payload binders resolve against the view's enum instead of silently binding nothing
// ("undefined identifier" at every use of x). The backend lowers the view to its enum
// handle before the tag test.
func conditionVariantPatternBase(expected Type) Type {
	if view, ok := expected.(*PackedVariantViewType); ok && view != nil && view.Enum != nil {
		return view.Enum
	}
	return expected
}

func (a *Analyzer) bindConditionStructPatternLocals(scope *Scope, pattern ast.MatchPattern, expected Type, valueExpr ast.Expr) {
	if a == nil || scope == nil || pattern == nil || expected == nil {
		return
	}
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern:
		return
	case *ast.MatchBindPattern:
		if p.Name == "_" {
			return
		}
		if a.analyzePredicateMatchPattern(p, expected, valueExpr) {
			return
		}
		sym := &Symbol{Name: p.Name, Kind: SymbolLocal, Type: expected, Node: p, Mutable: false}
		a.defineLocalInScope(scope, sym, p.Pos())
		if valueExpr != nil {
			a.recordValueBinding(sym, valueExpr)
			a.recordBorrowedOwnerRefBinding(sym, valueExpr)
			a.recordFunctionValueBinding(sym, valueExpr)
			a.recordImmutableSymbolOptimizationFacts(sym, valueExpr)
			a.recordRegionRefBinding(sym, valueExpr)
			a.recordPatternStorageViewBinding(sym, valueExpr)
		}
	case *ast.MatchStructPattern:
		if a.analyzeCountMatchPattern(p, expected) {
			return
		}
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil {
				continue
			}
			var fieldExpr ast.Expr
			if valueExpr != nil {
				fieldExpr = &ast.FieldExpr{Position: arg.Position, Object: valueExpr, Field: fields[i].Name}
			}
			a.bindConditionStructPatternLocals(scope, arg.Pattern, fields[i].Type, fieldExpr)
		}
	case *ast.MatchListPattern:
		elemType, ok := SequenceMatchElementType(expected)
		if !ok {
			return
		}
		for i, elem := range p.Elems {
			if _, ok := elem.(*ast.MatchRestPattern); ok {
				continue
			}
			var indexExpr ast.Expr
			if valueExpr != nil {
				indexExpr = &ast.IndexExpr{
					Position: elem.Pos(),
					Object:   valueExpr,
					Index:    &ast.IntLit{Position: elem.Pos(), Value: strconv.Itoa(i)},
				}
			}
			a.bindConditionStructPatternLocals(scope, elem, elemType, indexExpr)
		}
	case *ast.MatchOrPattern:
		if _, ok := a.collectOrPatternBindingTypes(p, expected); !ok || len(p.Options) == 0 {
			return
		}
		a.bindConditionStructPatternLocals(scope, p.Options[0], expected, valueExpr)
	case *ast.MatchVariantPattern:
		switch variantBase := conditionVariantPatternBase(expected).(type) {
		case *EnumType:
			variant, ok := variantBase.Variant(p.Variant)
			if !ok {
				return
			}
			orderedArgs := a.resolvePartialMatchPatternArgs(p, variant, variantBase.Name+"."+variant.Name, true)
			for i, arg := range orderedArgs {
				if arg == nil {
					continue
				}
				payloadExpr, restore := a.enterVariantPayloadPattern(valueExpr, p, moveBindVariantFieldKey(variant, i))
				a.bindConditionStructPatternLocals(scope, arg.Pattern, variant.Payload[i], payloadExpr)
				restore()
			}
		}
	}
}

func (a *Analyzer) analyzeBlockWithAffineClonePrepared(stmts []ast.Stmt, scope *Scope, prepare func()) affineFlowSnapshot {
	savedAffine := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	savedStorageViewDeps := a.currentStorageViewDeps
	savedAliasAccesses := a.currentAliasAccesses
	savedAliasBindings := a.currentAliasBindings
	savedAliasCarriers := a.currentAliasCarriers
	savedAliasCarrierFieldOverrides := a.currentAliasCarrierFieldOverrides
	savedPackedVariantViews := a.currentPackedVariantViews
	savedIndexBounds, savedIndexBoundsShared := a.currentIndexBounds, a.indexBoundsShared
	savedBoundEqual, savedBoundEqualShared := a.currentBoundEqual, a.boundEqualShared
	savedViewStaticLen := a.currentViewStaticLen
	savedViewMutable := a.currentViewMutable
	a.currentAffineValues = a.cloneAffineValueStates()
	a.currentBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
	a.currentFunctionValues = a.cloneFunctionValueBindings()
	a.currentSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
	a.currentValueBindings = a.cloneValueBindings()
	a.currentStorageViewDeps = a.cloneStorageViewDeps()
	a.currentAliasAccesses = a.cloneAliasAccesses()
	a.currentAliasBindings = a.cloneAliasBindings()
	a.currentAliasCarriers = a.cloneAliasCarriers()
	a.currentAliasCarrierFieldOverrides = a.cloneAliasCarrierFieldOverrides()
	// Copy-on-write: the block shares the entry maps until it first writes one.
	a.indexBoundsShared = true
	a.boundEqualShared = true
	a.currentViewStaticLen = cloneViewStaticLen(a.currentViewStaticLen)
	a.currentViewMutable = cloneViewMutable(a.currentViewMutable)
	if prepare != nil {
		prepare()
	}
	branchRegions := a.analyzeBlockWithRegionCloneCapture(stmts, scope)
	// The function-value map and its values were cloned on entry; the branch
	// result can take ownership without cloning them a second time.
	snapshot := affineFlowSnapshot{Regions: branchRegions, FunctionValues: a.currentFunctionValues, SpecializedValueTypes: a.snapshotPrivateSpecializedValueTypes(), RangeFacts: a.visibleRangeFacts(), AliasCarriers: a.currentAliasCarriers, AliasCarrierFieldOverrides: a.currentAliasCarrierFieldOverrides}
	blockEndRangeFacts(snapshot.RangeFacts, scope)
	if scope != nil {
		// Facts over branch-local immutable locals are rewritten through their initializers so they
		// survive the join (analyzer_smt_branch_local_facts.go).
		snapshot.SMTFacts = a.projectBranchLocalFacts(scope, append([]smtFact(nil), scope.smtAssertFacts...))
	}
	// The branch owns these maps from the entry clones. Transfer them to the result
	// after all branch processing, then restore the parent; no second copy is needed.
	snapshot.Affine = a.currentAffineValues
	snapshot.BorrowedOwnerRefs = a.currentBorrowedOwnerRefs
	snapshot.StorageViewDeps = a.currentStorageViewDeps
	snapshot.ValueBindings = a.currentValueBindings
	a.currentAffineValues = savedAffine
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	a.currentStorageViewDeps = savedStorageViewDeps
	a.currentAliasAccesses = savedAliasAccesses
	a.currentAliasBindings = savedAliasBindings
	a.currentAliasCarriers = savedAliasCarriers
	a.currentAliasCarrierFieldOverrides = savedAliasCarrierFieldOverrides
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentIndexBounds, a.indexBoundsShared = savedIndexBounds, savedIndexBoundsShared
	a.currentBoundEqual, a.boundEqualShared = savedBoundEqual, savedBoundEqualShared
	a.currentViewStaticLen = savedViewStaticLen
	a.currentViewMutable = savedViewMutable
	return snapshot
}

// blockEndRangeFacts adds to a branch snapshot the range facts the block's own scope holds at its
// end about names declared OUTSIDE the block (`r <- 0` inside `if c:` records r = [0, 0] there).
// The snapshot is taken after the block scope is left, so without this a variable the branch wrote
// is absent from the branch's facts and the if-join can only treat it as unknown; with it the join
// is the hull of the branch-end values. Facts about names the block itself declares are skipped
// (they are out of scope after the join and must not attach to a later same-named binding). A write
// in the block invalidates the fact across the scope chain, so a fact still present here holds at
// the block's end.
func blockEndRangeFacts(out map[string]numRange, scope *Scope) {
	if out == nil || scope == nil {
		return
	}
	for key, r := range scope.rangeFacts {
		root := key
		if i := strings.IndexByte(key, '.'); i >= 0 {
			root = key[:i]
		}
		if _, local := scope.Symbols[root]; local {
			continue
		}
		out[key] = r
	}
}
