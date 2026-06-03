package semantic

import "elisacore/src/ast"

// analyzePromoteStmt handles `promote <value> into <region>` — a copy-promote that
// relocates a value's own backing storage into a longer-lived region and re-types
// its provenance to name that region (docs/67).
//
// The type rule is conservatively shallow: the value's only region provenance must
// be its single top-level backing. Any second top-level region dependency, any
// packed-store dependency, or any interior (field/element) provenance is rejected,
// because promote's shallow copy relocates only the value's own storage and would
// leave such interior references dangling once the source region dies. Deep/graph
// promotion is future work.
func (a *Analyzer) analyzePromoteStmt(n *ast.PromoteStmt) {
	a.analyzeExpr(n.Value)

	dstSym, dstState := a.lookupRegionState(n.Region)
	if dstSym == nil {
		a.errorf(n.Pos(), "undefined region %q", n.Region)
		return
	}
	if dstState.Destroyed {
		a.errorf(n.Pos(), "region %q has already been destroyed", n.Region)
		return
	}

	valState, ok := a.regionRefStateForExpr(n.Value)
	srcSym, _, hasSrc := firstLiveRegionDependency(valState)
	if !ok || !hasSrc {
		a.errorf(n.Pos(), "promote: value is not region-backed; nothing to promote into %q", n.Region)
		return
	}

	liveTopLevelDeps := 0
	for _, dep := range valState.Deps {
		if dep.Valid {
			liveTopLevelDeps++
		}
	}
	if liveTopLevelDeps > 1 || len(valState.StoreDeps) != 0 || fieldsHaveRegionProvenance(valState) {
		a.errorf(n.Pos(), "promote: value contains interior references into a region; promote relocates only the value's own storage (deep/graph promotion is not supported)")
		return
	}

	if srcSym == dstSym {
		a.errorf(n.Pos(), "promote: value already lives in region %q", n.Region)
		return
	}

	// Transfer the backing provenance into the destination region. For a simple
	// local binding this re-types the binding in place (it now lives in dst).
	newState, _ := rebaseRegionDependencyInState(valState, srcSym, dstSym, dstState.Generation)
	if sym := a.promoteTargetSymbol(n.Value); sym != nil {
		a.currentRegionRefs[sym] = newState
	}
}

// promoteTargetSymbol resolves a promote value expression to the local binding
// whose provenance should be re-typed, when the value is a simple binding.
func (a *Analyzer) promoteTargetSymbol(expr ast.Expr) *Symbol {
	for {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.Ident:
			if a.currentScope == nil {
				return nil
			}
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				return sym
			}
			return nil
		default:
			return nil
		}
	}
}

// fieldsHaveRegionProvenance reports whether any field/element of a value (at any
// depth) carries region or packed-store provenance — i.e. an interior reference
// that promote's shallow copy would not relocate.
func fieldsHaveRegionProvenance(state regionRefState) bool {
	for _, field := range state.Fields {
		if hasRegionDependencies(field) || fieldsHaveRegionProvenance(field) {
			return true
		}
	}
	return false
}
