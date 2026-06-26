package semantic

import (
	"elisacore/src/ast"
)

// checkCalleeSpecSignatureWherePredicates discharges anonymous `T where P` preconditions that are
// recorded in a callee's SpecSignature.ParamPredicates (the canonical interface summary path).
//
// This complements checkCalleeParamWhereRefinements, which reads the predicate from the callee's
// AST params[i].Type. When the callee has no AST (imported / cross-module), the AST SourceType
// stored in the SpecBinder may be nil or stripped, so the AST path cannot fire. This function
// works purely from the already-extracted SpecSignature.ParamPredicates entries whose SourceExpr
// is set (the where-bool-expr channel).
//
// Deduplication: when a param's scheme.Params[i].Type is a WhereRefinementTypeExpr, the AST path
// (checkCalleeParamWhereRefinements) already handles that obligation; skip it here to avoid
// double-reporting the same diagnostic.
func (a *Analyzer) checkCalleeSpecSignatureWherePredicates(call *ast.CallExpr, declName string, sig *SpecSignature, schemeParams []ast.ParamDecl, args []ast.Expr) {
	if a == nil || call == nil || sig == nil {
		return
	}

	// Build a substitution from binder position → actual call argument, mirroring
	// checkCalleeParamWhereRefinements which builds subst from param name → arg.
	// We key by binder.Position (param index) so we can substitute the binder's
	// SourceName in the predicate expression.
	paramNameByPos := make(map[int]string, len(sig.Params))
	for _, binder := range sig.Params {
		if binder.Kind == SpecBinderParam && binder.Position >= 0 {
			paramNameByPos[binder.Position] = binder.SourceName
		}
	}

	subst := map[string]ast.Expr{}
	for pos, name := range paramNameByPos {
		if pos < len(args) && args[pos] != nil {
			subst[name] = args[pos]
		}
	}

	for _, pred := range sig.ParamPredicates {
		if pred.Kind != RefinementPredicateType {
			continue
		}
		// Only handle anonymous where-bool-expr predicates: SourceExpr set, LawName empty.
		sourceExpr, ok := pred.SourceExpr.(ast.Expr)
		if !ok || sourceExpr == nil || pred.LawName != "" {
			continue
		}
		paramIdx := pred.Subject.Position
		if pred.Subject.Kind != SpecBinderParam || paramIdx < 0 {
			continue
		}

		// Deduplication: if the scheme's AST param at this position already carries a
		// WhereRefinementTypeExpr WITH a real predicate, checkCalleeParamWhereRefinements
		// discharges that obligation — skip here to avoid double-reporting.
		//
		// Soundness note: we must only skip when the AST path will ACTUALLY fire. The AST path
		// (checkCalleeParamWhereRefinements) bails out when rt.Predicate == nil, so a where-type
		// carrying a nil predicate is NOT covered there. Skipping on a bare `isWhere` would drop
		// the obligation entirely (the SpecSignature carries the live predicate). Require a
		// non-nil AST predicate before deferring. For an imported callee the SourceType is nil or
		// stripped, so this never matches and the SpecSignature path remains fully in force.
		if paramIdx >= 0 && paramIdx < len(schemeParams) {
			if rt, isWhere := whereRefinementTypeExpr(schemeParams[paramIdx].Type); isWhere && rt != nil && rt.Predicate != nil {
				continue
			}
		}

		a.dischargeWherePredicate(sourceExpr, subst, call.Pos(), "where precondition of "+declName)
	}
}
