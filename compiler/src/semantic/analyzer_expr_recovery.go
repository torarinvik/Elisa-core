package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) analyzeRewriteDefaultIdent(expr *ast.Ident) (Type, bool) {
	if expr == nil || expr.Name != "default" {
		return nil, false
	}
	if a.currentRewriteDefault == nil {
		a.errorf(expr.Pos(), "default is only allowed inside a rewrite arm body")
		return invalidType, true
	}
	if !a.currentRewriteDefault.Allowed || a.currentRewriteDefault.ResultType == nil {
		message := a.currentRewriteDefault.Message
		if message == "" {
			message = "default is only allowed inside an exact tree rewrite arm"
		}
		a.errorf(expr.Pos(), "%s", message)
		return invalidType, true
	}
	if a.rewriteDefaults != nil {
		a.rewriteDefaults[expr] = true
	}
	return a.currentRewriteDefault.ResultType, true
}
func (a *Analyzer) rewriteDefaultExactType(expr ast.Expr) (Type, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n.Name != "default" || a.currentRewriteDefault == nil || !a.currentRewriteDefault.Allowed || a.currentRewriteDefault.ExactType == nil {
			return nil, false
		}
		return a.currentRewriteDefault.ExactType, true
	case *ast.ParenExpr:
		return a.rewriteDefaultExactType(n.Inner)
	default:
		return nil, false
	}
}
func (a *Analyzer) analyzeExprBlock(expr *ast.ExprBlock) Type {
	return a.analyzeExprBlockWithExpected(expr, nil)
}

// analyzeExprBlockWithExpected analyzes a value block whose destination type is known.
// The tail is the block's value, so it is checked exactly as the same expression would
// be in the destination's own position: `s: sview =` NEWLINE INDENT … `"lit"` adapts
// the literal like `s: sview = "lit"` does, and a `null`/integer/float tail takes the
// destination's optional/width. Without this, wrapping a valid initializer in a block
// (the docs/119 §2 refactoring) turned it into a type error.
func (a *Analyzer) analyzeExprBlockWithExpected(expr *ast.ExprBlock, expected Type) Type {
	if expr == nil {
		return invalidType
	}
	// A diverging branch block (docs/119 §4.1): no value, its last statement leaves.
	diverging := expr.Value == nil
	if diverging && (len(expr.Stmts) == 0 || !stmtDefinitelyExits(expr.Stmts[len(expr.Stmts)-1])) {
		return invalidType
	}
	savedScope := a.currentScope
	a.currentScope = NewScope(savedScope)
	// docs/119 E4: a value block is pure over outer state — reject direct writes to
	// enclosing bindings before analyzing the body (locals aren't defined yet, so an
	// outer name still resolves to its ancestor-scope binding). The returned allowed-set
	// is pushed so the mutating-CALL half of E4 can consult it during body analysis.
	allowed := a.checkValueBlockOuterMutation(expr)
	a.valueBlockAllowed = append(a.valueBlockAllowed, allowed)
	a.valueBlockLoopDepths = append(a.valueBlockLoopDepths, a.loopDepth)
	var result Type
	for _, stmt := range expr.Stmts {
		a.analyzeStmt(stmt)
	}
	if diverging {
		result = neverType
	} else if expected != nil {
		result = a.analyzeValueExpr(expr.Value, expected)
	} else {
		result = a.analyzeExpr(expr.Value)
	}
	a.valueBlockLoopDepths = a.valueBlockLoopDepths[:len(a.valueBlockLoopDepths)-1]
	a.valueBlockAllowed = a.valueBlockAllowed[:len(a.valueBlockAllowed)-1]
	a.currentScope = savedScope
	return result
}

// breakLeavesValueBlock reports whether a `break`/`continue` at the current loop depth
// would jump out of the innermost value block (docs/119 §2.2 rule 3, E5): the block was
// entered inside a loop and no loop has been opened within it since. A loop EXPRESSION
// keeps its own `break`/`continue` legal because its loop statement sits inside the block.
func (a *Analyzer) breakLeavesValueBlock() bool {
	n := len(a.valueBlockLoopDepths)
	return n > 0 && a.loopDepth > 0 && a.loopDepth == a.valueBlockLoopDepths[n-1]
}

func recoveryClauseForExpr(recovery *ast.RecoveryClause, fallback ast.Expr, pos lexer.Pos) *ast.RecoveryClause {
	if recovery != nil {
		return recovery
	}
	if fallback == nil {
		return nil
	}
	if raise, ok := fallback.(*ast.RaiseExpr); ok {
		return &ast.RecoveryClause{Position: fallback.Pos(), Kind: ast.RecoveryRaise, Value: raise.Error}
	}
	return &ast.RecoveryClause{Position: pos, Kind: ast.RecoveryValue, Value: fallback}
}

func (a *Analyzer) analyzeRecoveryClause(recovery *ast.RecoveryClause, expected Type, errorType Type, label string) Type {
	if recovery == nil {
		return invalidType
	}
	// A recovery that leaves the function (`else return`, `else raise`, a block
	// that always exits) never reaches the code after the expression, so what it
	// consumes must not flow into the fall-through affine state.
	fallThrough := a.cloneAffineValueStates()
	switch recovery.Kind {
	case ast.RecoveryValue:
		return a.analyzeExpr(recovery.Value)
	case ast.RecoveryRaise:
		a.analyzeExpr(&ast.RaiseExpr{Position: recovery.Position, Error: recovery.Value})
		a.currentAffineValues = fallThrough
		return neverType
	case ast.RecoveryReturn:
		a.analyzeRecoveryReturn(recovery)
		a.currentAffineValues = fallThrough
		return neverType
	case ast.RecoveryVoid:
		if expected != nil && !isVoidType(expected) && !IsInvalidType(expected) {
			a.errorf(recovery.Position, "%s cannot use else void for non-void result %s", label, expected)
		}
		return a.namedTypes["void"]
	case ast.RecoveryBlock:
		scope := NewScope(a.currentScope)
		if recovery.Binding != "" {
			if errorType == nil {
				a.errorf(recovery.Position, "else error binding requires an error-union operand")
				errorType = invalidType
			}
			a.defineLocalInScope(scope, &Symbol{Name: recovery.Binding, Kind: SymbolLocal, Type: errorType, Mutable: false}, recovery.Position)
		}
		a.analyzeBlockInScope(recovery.Body, scope)
		if blockDefinitelyExits(recovery.Body) {
			a.currentAffineValues = fallThrough
			return neverType
		}
		if expected != nil && !isVoidType(expected) && !IsInvalidType(expected) {
			a.errorf(recovery.Position, "%s block must return, raise, or produce %s", label, expected)
		}
		return a.namedTypes["void"]
	default:
		return invalidType
	}
}

func (a *Analyzer) analyzeRecoveryReturn(recovery *ast.RecoveryClause) {
	if a.currentReturn == nil {
		a.errorf(recovery.Position, "return recovery is only valid inside a function")
		if recovery.Value != nil {
			a.analyzeExpr(recovery.Value)
		}
		return
	}
	if recovery.Value == nil {
		if !isVoidType(a.currentReturn) {
			a.errorf(recovery.Position, "return recovery expects %s, got void", a.currentReturn)
		}
		// `else return` leaves the function: live must-consume values leak.
		a.reportUnconsumedProtocolValuesOnExit()
		return
	}
	valueType := a.analyzeExpr(recovery.Value)
	expectedReturn := a.matchReturnType(valueType)
	if !AssignableTo(expectedReturn, valueType) {
		a.errorf(recovery.Position, "return type expects %s, got %s", expectedReturn, valueType)
		a.reportShapeMismatchNotes(recovery.Position, expectedReturn, valueType)
	}
	a.consumeAffineValueExpr(recovery.Value, expectedReturn, "return")
	a.reportUnconsumedProtocolValuesOnExit()
}

// narrowedOptionalDeclaredType returns the declared optional type of a place that an
// assignment narrowed to its payload. See Scope.narrowedOptionals.
func (a *Analyzer) narrowedOptionalDeclaredType(place ast.Expr) (Type, bool) {
	if a.currentScope == nil || place == nil {
		return nil, false
	}
	key, ok := exprRefinementKey(place)
	if !ok {
		return nil, false
	}
	return a.currentScope.LookupNarrowedOptional(key)
}
