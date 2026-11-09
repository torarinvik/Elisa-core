package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) applyPostIfFallthroughRefinement(stmt *ast.IfStmt) {
	if a.currentScope == nil || len(stmt.Elifs) > 0 {
		return
	}
	if blockDefinitelyExits(stmt.Then) {
		// The then-branch returns, so reaching here means the condition was FALSE. Apply the negated
		// refinements AND record the negated condition as an assumable SMT fact — this is the legitimate
		// fall-through guard (`if a == 0: return` ⟹ `a != 0` afterward; `if p == null: return` ⟹
		// `p != null`). Without recording it, the only condition fact was the truthy one from the branch,
		// so an SMT guard proof had to rely on a stale/contradictory leak (now fixed).
		a.applyConditionRefinements(a.currentScope, stmt.Cond, false)
		a.recordSMTAssertFact(smtFactExprForCondition(stmt.Cond, false))
	}
	if len(stmt.Else) > 0 && blockDefinitelyExits(stmt.Else) {
		a.applyConditionRefinements(a.currentScope, stmt.Cond, true)
		a.recordSMTAssertFact(smtFactExprForCondition(stmt.Cond, true))
	}
}

// MatchPatternIsIrrefutable reports whether a pattern matches every value of its
// scrutinee's type: `_`, or a struct pattern whose every field either binds or is itself
// irrefutable. A struct has exactly one shape, so destructuring it always succeeds; a
// field carrying a LITERAL is a test and makes the arm refutable. At the TOP level a bare
// name is not counted — it may name an enum variant or a constant rather than bind.
func MatchPatternIsIrrefutable(pattern ast.MatchPattern) bool {
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern:
		return true
	case *ast.MatchStructPattern:
		if p == nil {
			return false
		}
		for _, arg := range p.Args {
			if arg.Pattern == nil {
				continue
			}
			if _, ok := arg.Pattern.(*ast.MatchBindPattern); ok {
				continue
			}
			if !MatchPatternIsIrrefutable(arg.Pattern) {
				return false
			}
		}
		return true
	}
	return false
}

func blockDefinitelyExits(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	return stmtDefinitelyExits(stmts[len(stmts)-1])
}
func stmtDefinitelyExits(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt, *ast.PanicStmt, *ast.StaticErrorStmt:
		return true
	case *ast.ExprStmt:
		_, ok := n.Expr.(*ast.RaiseExpr)
		return ok
	case *ast.IfStmt:
		if !blockDefinitelyExits(n.Then) {
			return false
		}
		for _, elif := range n.Elifs {
			if !blockDefinitelyExits(elif.Body) {
				return false
			}
		}
		return len(n.Else) > 0 && blockDefinitelyExits(n.Else)
	case *ast.MatchStmt:
		if len(n.Arms) == 0 {
			return false
		}
		hasWildcard := false
		for _, arm := range n.Arms {
			// docs/122 §5.1: a guarded catch-all can fail, so the match may fall through.
			// An irrefutable struct pattern (`Item{v}:` — every field a binding or `_`)
			// always matches its struct scrutinee, exactly like `_`.
			if arm.Guard == nil && MatchPatternIsIrrefutable(arm.Pattern) {
				hasWildcard = true
			}
			if !blockDefinitelyExits(arm.Body) {
				return false
			}
		}
		return hasWildcard
	case *ast.StaticIfStmt:
		if !blockDefinitelyExits(n.Then) {
			return false
		}
		for _, elif := range n.Elifs {
			if !blockDefinitelyExits(elif.Body) {
				return false
			}
		}
		return len(n.Else) > 0 && blockDefinitelyExits(n.Else)
	case *ast.PoolStmt:
		return blockDefinitelyExits(n.Body)
	case *ast.LockStmt:
		return blockDefinitelyExits(n.Body)
	default:
		return false
	}
}
func (a *Analyzer) recordAssignmentRefinement(target ast.Expr, targetType Type, valueType Type) {
	if a.currentScope == nil {
		return
	}
	key, ok := exprRefinementKey(target)
	if !ok {
		return
	}
	if declared, current, ok := a.writeThroughRefTarget(target, targetType); ok && current != declared {
		// `r <- v` on a `mutable T&` binding stores THROUGH the reference: the pointer, and so its
		// proven null state, is unchanged. Only what was known about the referent is stale.
		// (With no refinement in force there is nothing to keep: the path below is unchanged.)
		a.currentScope.setRefinement(key, cloneRefTypeWithState(declared, current.State))
		a.currentScope.SetNarrowedOptional(key, nil)
		return
	}
	refined := assignedRefinementType(targetType, valueType)
	if refined == nil {
		delete(a.currentScope.Refinements, key)
		a.currentScope.SetNarrowedOptional(key, nil)
		return
	}
	a.currentScope.setRefinement(key, refined)
	// Remember the optional we narrowed AWAY from, so absence can still be asked about.
	// nil on every other outcome, which also shadows a stale entry from an outer scope
	// (`x <- 5` in a branch, then `x <- null`).
	var narrowedFrom Type
	if targetOptional, ok := targetType.(*OptionalType); ok && refined == targetOptional.Value {
		narrowedFrom = targetOptional
	}
	a.currentScope.SetNarrowedOptional(key, narrowedFrom)
}

// writeThroughRefTarget reports whether assigning to target writes through an immutable
// `mutable T&` / `mutable T&?` binding (assignmentTargetType resolved it to the referent), and
// returns the declared reference with its current flow-refined form.
func (a *Analyzer) writeThroughRefTarget(target ast.Expr, targetType Type) (*RefType, *RefType, bool) {
	for {
		paren, ok := target.(*ast.ParenExpr)
		if !ok {
			break
		}
		target = paren.Inner
	}
	ident, ok := target.(*ast.Ident)
	if !ok || a.currentScope == nil {
		return nil, nil, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return nil, nil, false
	}
	declared, ok := sym.Type.(*RefType)
	if !ok || declared == nil || !declared.Mutable {
		return nil, nil, false
	}
	// A rebind (`x <- &w` on a mutable binding) is typed as the reference itself; a store
	// through it is typed as the referent (an immutable binding always, a mutable binding when
	// the RHS is a scalar).
	if _, targetIsRef := targetType.(*RefType); targetIsRef && SameType(targetType, declared) {
		return nil, nil, false
	}
	return declared, a.currentRefType(ident, declared), true
}

func assignedRefinementType(targetType Type, valueType Type) Type {
	targetRef, ok := targetType.(*RefType)
	if ok {
		if IsNullType(valueType) {
			return cloneRefTypeWithState(targetRef, RefStateNull)
		}
		if valueRef, ok := valueType.(*RefType); ok {
			return cloneRefType(valueRef)
		}
		return targetRef
	}
	targetOptional, ok := targetType.(*OptionalType)
	if !ok {
		return nil
	}
	if IsNullType(valueType) {
		return nullType
	}
	if _, ok := valueType.(*OptionalType); ok {
		return targetOptional
	}
	if AssignableTo(targetOptional.Value, valueType) {
		return targetOptional.Value
	}
	return targetOptional
}
func (a *Analyzer) assertedCondition(expr ast.Expr) (ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || (ident.Name != "assert" && ident.Name != "ASSERT") || a == nil || a.currentScope == nil {
		return nil, false
	}
	if _, shadowed := a.currentScope.Lookup(ident.Name); shadowed {
		return nil, false
	}
	return call.Args[0], true
}
func (a *Analyzer) analyzeExprInScope(expr ast.Expr, scope *Scope) Type {
	saved := a.currentScope
	a.currentScope = scope
	result := a.analyzeExpr(expr)
	a.currentScope = saved
	return result
}
func (a *Analyzer) analyzeExprInAffineScope(expr ast.Expr, scope *Scope) (Type, affineFlowSnapshot) {
	return a.analyzeExprInAffineScopePrepared(expr, scope, nil)
}
func (a *Analyzer) analyzeExprInConditionAffineScope(expr ast.Expr, parent *Scope, cond ast.Expr, truthy bool) (Type, affineFlowSnapshot) {
	scope := a.refinedScopeForCondition(parent, cond, truthy)
	return a.analyzeExprInAffineScopePrepared(expr, scope, func() {
		a.applyConditionRefinementsInternal(scope, cond, truthy, true)
	})
}
func (a *Analyzer) analyzeExprInAffineScopePrepared(expr ast.Expr, scope *Scope, prepare func()) (Type, affineFlowSnapshot) {
	savedAffine := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	a.currentAffineValues = a.cloneAffineValueStates()
	a.currentBorrowedOwnerRefs = a.cloneBorrowedOwnerRefBindings()
	a.currentFunctionValues = a.cloneFunctionValueBindings()
	a.currentSpecializedValueTypes = a.cloneSpecializedValueTypeBindings()
	a.currentValueBindings = a.cloneValueBindings()
	if prepare != nil {
		prepare()
	}
	result := a.analyzeExprInScope(expr, scope)
	// Entry clones belong to this path; transfer them before restoring the parent.
	snapshot := affineFlowSnapshot{Affine: a.currentAffineValues, BorrowedOwnerRefs: a.currentBorrowedOwnerRefs, FunctionValues: a.currentFunctionValues, SpecializedValueTypes: a.snapshotPrivateSpecializedValueTypes(), ValueBindings: a.currentValueBindings, AliasCarriers: a.cloneAliasCarriers(), AliasCarrierFieldOverrides: a.cloneAliasCarrierFieldOverrides()}
	a.currentAffineValues = savedAffine
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	return result, snapshot
}
