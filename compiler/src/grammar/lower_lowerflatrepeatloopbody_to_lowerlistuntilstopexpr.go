package grammar

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (ctx *statefulLowerContext) lowerFlatRepeatLoopBody(term *ast.GrammarFlatRepeatTerm, itemSnapshot string, resultName string, stopName string, matchedName string, committedName string, itemAttempt loweredAttempt) []ast.Stmt {
	continueBody := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: itemSnapshot, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
	}
	continueBody = append(continueBody, itemAttempt.Stmts...)
	continueBody = append(continueBody, markCommittedStmts(committedName, term.Position, itemAttempt.Committed)...)
	groupName := ctx.fresh("flatrepeat_group")
	continueBody = append(continueBody, &ast.IfStmt{
		Position: term.Position,
		Cond:     itemAttempt.Matched,
		Then: append([]ast.Stmt{
			&ast.VarDeclStmt{Position: term.Position, Name: groupName, Value: itemAttempt.Value},
		}, listPushIndexedItemsStmts(ctx, term.Position, resultName, groupName)...),
		Else: []ast.Stmt{&ast.IfStmt{Position: term.Position, Cond: itemAttempt.Committed, Then: []ast.Stmt{&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: matchedName}, Value: &ast.BoolLit{Position: term.Position, Value: false}}, &ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: stopName}, Value: &ast.BoolLit{Position: term.Position, Value: true}}}, Else: []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, itemSnapshot, term.Position), &ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: stopName}, Value: &ast.BoolLit{Position: term.Position, Value: true}}}}},
	})
	if stopCond := ctx.lowerListUntilMatchExpr(term.Position, term.Until); stopCond != nil {
		return []ast.Stmt{&ast.IfStmt{
			Position: term.Position,
			Cond:     stopCond,
			Then: []ast.Stmt{
				&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: stopName}, Value: &ast.BoolLit{Position: term.Position, Value: true}},
			},
			Else: continueBody,
		}}
	}
	return continueBody
}
func (ctx *statefulLowerContext) lowerSingletonAttempt(term *ast.GrammarSingletonTerm) loweredAttempt {
	elemType := grammarListElementType(term.Position, term.Type, grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType))
	resultType := listTypeExpr(term.Position, elemType)
	resultName := ctx.fresh("singleton_value")
	resultInit := ast.Expr(&ast.ListLitExpr{Position: term.Position})
	body := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: resultName, Mutable: true, Type: resultType, Value: resultInit},
		listPushStmt(term.Position, resultName, term.Value),
	}
	if ctx.allocExpr != nil {
		resultInit = zeroedCastExpr(term.Position, resultType)
		body = []ast.Stmt{
			&ast.VarDeclStmt{Position: term.Position, Name: resultName, Mutable: true, Type: resultType, Value: resultInit},
			&ast.InStoreStmt{
				Position: term.Position,
				Store:    ctx.allocExpr,
				Body: []ast.Stmt{
					&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: resultName}, Value: &ast.ListLitExpr{Position: term.Position}},
					listPushStmt(term.Position, resultName, term.Value),
				},
			},
		}
	}
	return loweredAttempt{Stmts: body, Matched: &ast.BoolLit{Position: term.Position, Value: true}, Committed: &ast.BoolLit{Position: term.Position, Value: false}, Value: &ast.Ident{Position: term.Position, Name: resultName}}
}
func (ctx *statefulLowerContext) lowerEmptyAttempt(term *ast.GrammarEmptyTerm) loweredAttempt {
	elemType := grammarListElementType(term.Position, term.Type, grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType))
	resultType := listTypeExpr(term.Position, elemType)
	resultName := ctx.fresh("empty_value")
	body := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: resultName, Type: resultType, Value: &ast.ListLitExpr{Position: term.Position}},
	}
	return loweredAttempt{Stmts: body, Matched: &ast.BoolLit{Position: term.Position, Value: true}, Committed: &ast.BoolLit{Position: term.Position, Value: false}, Value: &ast.Ident{Position: term.Position, Name: resultName}}
}
func (ctx *statefulLowerContext) lowerConcatAttempt(term *ast.GrammarConcatTerm) loweredAttempt {
	if term == nil || len(term.Terms) == 0 {
		pos := ctx.production.Position
		resultType := grammarResolvedValueTypeExpr(pos, ctx.production.ReturnType)
		if resultType == nil {
			resultType = listTypeExpr(pos, builtinTypeExpr(pos, "bool"))
		}
		valueName := ctx.fresh("concat_value")
		resultInit := ast.Expr(&ast.ListLitExpr{Position: pos})
		stmts := []ast.Stmt{}
		if ctx.allocExpr != nil {
			resultInit = zeroedCastExpr(pos, resultType)
			stmts = append(stmts, &ast.VarDeclStmt{Position: pos, Name: valueName, Mutable: true, Type: resultType, Value: resultInit})
			stmts = append(stmts, &ast.InStoreStmt{Position: pos, Store: ctx.allocExpr, Body: []ast.Stmt{&ast.AssignStmt{Position: pos, Target: &ast.Ident{Position: pos, Name: valueName}, Value: &ast.ListLitExpr{Position: pos}}}})
		} else {
			stmts = append(stmts, &ast.VarDeclStmt{Position: pos, Name: valueName, Mutable: true, Type: resultType, Value: resultInit})
		}
		return loweredAttempt{Stmts: stmts, Matched: &ast.BoolLit{Position: pos, Value: true}, Committed: &ast.BoolLit{Position: pos, Value: false}, Value: &ast.Ident{Position: pos, Name: valueName}}
	}
	if ctx.cursorReceiver == "" || ctx.cursorField == "" {
		valueName := ctx.fresh("concat_value")
		resultType := ctx.termValueTypeOrProduction(term.Position, term)
		return loweredAttempt{
			Stmts:     []ast.Stmt{&ast.VarDeclStmt{Position: term.Position, Name: valueName, Type: resultType, Value: lowerConcatExpr(ctx.lowerExprContext(), term)}},
			Matched:   &ast.BoolLit{Position: term.Position, Value: true},
			Committed: &ast.BoolLit{Position: term.Position, Value: false},
			Value:     &ast.Ident{Position: term.Position, Name: valueName},
		}
	}
	resultType := ctx.termValueTypeOrProduction(term.Position, term)
	snapshotName := ctx.fresh("concat_cursor")
	matchedName := ctx.fresh("concat_matched")
	committedName := ctx.fresh("concat_committed")
	resultName := ctx.fresh("concat_value")
	body := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: snapshotName, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
		&ast.VarDeclStmt{Position: term.Position, Name: matchedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.VarDeclStmt{Position: term.Position, Name: committedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
	}
	resultInit := ast.Expr(&ast.ListLitExpr{Position: term.Position})
	concatCtx := *ctx
	concatCtx.committedName = committedName
	concatStmts := concatCtx.lowerConcatTerms(term.Terms, snapshotName, matchedName, resultName, resultType)
	ctx.tempCounter = concatCtx.tempCounter
	if ctx.allocExpr != nil {
		resultInit = zeroedCastExpr(term.Position, resultType)
		concatStmts = []ast.Stmt{&ast.InStoreStmt{
			Position: term.Position,
			Store:    ctx.allocExpr,
			Body: append([]ast.Stmt{
				&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: resultName}, Value: &ast.ListLitExpr{Position: term.Position}},
			}, concatStmts...),
		}}
	}
	body = append(body, &ast.VarDeclStmt{Position: term.Position, Name: resultName, Mutable: true, Type: resultType, Value: resultInit})
	body = append(body, concatStmts...)
	return loweredAttempt{Stmts: body, Matched: &ast.Ident{Position: term.Position, Name: matchedName}, Committed: &ast.Ident{Position: term.Position, Name: committedName}, Value: &ast.Ident{Position: term.Position, Name: resultName}}
}
func (ctx *statefulLowerContext) lowerConcatTerms(terms []ast.GrammarTerm, snapshotName string, matchedName string, resultName string, resultType ast.TypeExpr) []ast.Stmt {
	if len(terms) == 0 {
		return []ast.Stmt{&ast.AssignStmt{Position: ctx.production.Position, Target: &ast.Ident{Position: ctx.production.Position, Name: matchedName}, Value: &ast.BoolLit{Position: ctx.production.Position, Value: true}}}
	}
	current := terms[0]
	pos := current.Pos()
	attempt := ctx.lowerAttempt(current)
	groupType := ctx.termValueTypeOrProduction(pos, current)
	if groupType == nil {
		groupType = resultType
	}
	groupValue := attempt.Value
	if groupValue == nil {
		groupValue = zeroedCastExpr(pos, groupType)
	}
	groupName := ctx.fresh("concat_group")
	thenBranch := append(ctx.markAttemptCommittedStmts(pos, attempt.Committed),
		&ast.VarDeclStmt{Position: pos, Name: groupName, Type: groupType, Value: groupValue},
	)
	thenBranch = append(thenBranch, listPushIndexedItemsStmts(ctx, pos, resultName, groupName)...)
	if len(terms) == 1 {
		thenBranch = append(thenBranch, &ast.AssignStmt{Position: pos, Target: &ast.Ident{Position: pos, Name: matchedName}, Value: &ast.BoolLit{Position: pos, Value: true}})
	} else {
		thenBranch = append(thenBranch, ctx.lowerConcatTerms(terms[1:], snapshotName, matchedName, resultName, resultType)...)
	}
	prefix := append([]ast.Stmt{}, attempt.Stmts...)
	prefix = append(prefix, &ast.IfStmt{Position: pos, Cond: attempt.Matched, Then: thenBranch, Else: []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, pos)}})
	return prefix
}
func (ctx *statefulLowerContext) lowerChoiceOptions(options []ast.GrammarTerm, snapshotName string, matchedName string, committedName string, valueName string, targetType ast.TypeExpr) []ast.Stmt {
	if len(options) == 0 {
		return []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, ctx.production.Position)}
	}
	attempt := ctx.lowerAttempt(options[0])
	facts := ctx.termFacts(options[0])
	value := grammarCoerceValueToType(options[0].Pos(), attempt.Value, facts.ValueType, targetType)
	thenBranch := append(markCommittedStmts(committedName, options[0].Pos(), attempt.Committed),
		&ast.AssignStmt{Position: options[0].Pos(), Target: &ast.Ident{Position: options[0].Pos(), Name: matchedName}, Value: &ast.BoolLit{Position: options[0].Pos(), Value: true}},
		&ast.AssignStmt{Position: options[0].Pos(), Target: &ast.Ident{Position: options[0].Pos(), Name: valueName}, Value: value},
	)
	fallback := []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, options[0].Pos())}
	if len(options) > 1 {
		fallback = append(fallback, ctx.lowerChoiceOptions(options[1:], snapshotName, matchedName, committedName, valueName, targetType)...)
	}
	result := append([]ast.Stmt{}, attempt.Stmts...)
	result = append(result, &ast.IfStmt{Position: options[0].Pos(), Cond: attempt.Matched, Then: thenBranch, Else: []ast.Stmt{&ast.IfStmt{Position: options[0].Pos(), Cond: attempt.Committed, Then: []ast.Stmt{committedAssignTrueStmt(committedName, options[0].Pos())}, Else: fallback}}})
	return result
}
func (ctx *statefulLowerContext) lowerOptionalAttempt(term *ast.GrammarOptionalTerm) loweredAttempt {
	inner := ctx.lowerAttempt(term.Term)
	innerFacts := ctx.termFacts(term.Term)
	snapshotName := ctx.fresh("optional_cursor")
	innerType := innerFacts.ValueType
	termType := optionalTypeExpr(term.Position, innerType)
	matchedName := ctx.fresh("optional_matched")
	committedName := ctx.fresh("optional_committed")
	valueName := ctx.fresh("optional_value")
	stms := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: snapshotName, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
		&ast.VarDeclStmt{Position: term.Position, Name: matchedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: true}},
		&ast.VarDeclStmt{Position: term.Position, Name: committedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.VarDeclStmt{Position: term.Position, Name: valueName, Mutable: true, Type: termType, Value: nullOptionalExpr(term.Position, innerType)},
	}
	stms = append(stms, inner.Stmts...)
	stms = append(stms, &ast.IfStmt{Position: term.Position, Cond: inner.Matched, Then: append(markCommittedStmts(committedName, term.Position, inner.Committed), &ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: valueName}, Value: presentOptionalExpr(term.Position, inner.Value, innerType)}), Else: []ast.Stmt{&ast.IfStmt{Position: term.Position, Cond: inner.Committed, Then: []ast.Stmt{&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: matchedName}, Value: &ast.BoolLit{Position: term.Position, Value: false}}, committedAssignTrueStmt(committedName, term.Position)}, Else: []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, term.Position)}}}})
	return loweredAttempt{
		Stmts:     stms,
		Matched:   &ast.Ident{Position: term.Position, Name: matchedName},
		Committed: &ast.Ident{Position: term.Position, Name: committedName},
		Value:     &ast.Ident{Position: term.Position, Name: valueName},
	}
}
func (ctx *statefulLowerContext) lowerPrecedenceAttempt(term *ast.GrammarPrecedenceTerm) loweredAttempt {
	if len(term.Levels) != 0 {
		return ctx.lowerNamedPrecedenceAttempt(term)
	}
	seedAttempt := ctx.lowerAttempt(term.Seed)
	leftType := ctx.termValueTypeOrProduction(term.Position, term.Seed)
	stopName := ctx.fresh("precedence_stop")
	matchedName := ctx.fresh("precedence_matched")
	committedName := ctx.fresh("precedence_committed")
	snapshotName := ctx.fresh("precedence_cursor")
	leftName := term.LeftName
	stmts := append([]ast.Stmt{}, seedAttempt.Stmts...)
	stmts = append(stmts,
		&ast.VarDeclStmt{Position: term.Position, Name: matchedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: seedAttempt.Matched},
		&ast.VarDeclStmt{Position: term.Position, Name: committedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
	)
	stmts = append(stmts, markCommittedStmts(committedName, term.Position, seedAttempt.Committed)...)
	stmts = append(stmts,
		&ast.VarDeclStmt{Position: term.Position, Name: leftName, Mutable: true, Type: leftType, Value: seedAttempt.Value},
		&ast.VarDeclStmt{Position: term.Position, Name: stopName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.WhileStmt{
			Position: term.Position,
			Cond:     &ast.UnaryExpr{Position: term.Position, Op: lexer.TOKEN_NOT, Operand: &ast.Ident{Position: term.Position, Name: stopName}},
			Body: append([]ast.Stmt{
				&ast.VarDeclStmt{Position: term.Position, Name: snapshotName, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
			}, ctx.lowerPrecedenceArms(term.Arms, snapshotName, stopName, matchedName, committedName, leftName, term.Assoc)...),
		},
	)
	return loweredAttempt{Stmts: stmts, Matched: &ast.Ident{Position: term.Position, Name: matchedName}, Committed: &ast.Ident{Position: term.Position, Name: committedName}, Value: &ast.Ident{Position: term.Position, Name: leftName}}
}
func (ctx *statefulLowerContext) lowerPostfixAttempt(term *ast.GrammarPostfixTerm) loweredAttempt {
	seedAttempt := ctx.lowerAttempt(term.Seed)
	leftType := ctx.termValueTypeOrProduction(term.Position, term.Seed)
	stopName := ctx.fresh("postfix_stop")
	matchedName := ctx.fresh("postfix_matched")
	committedName := ctx.fresh("postfix_committed")
	snapshotName := ctx.fresh("postfix_cursor")
	leftName := term.LeftName
	stmts := append([]ast.Stmt{}, seedAttempt.Stmts...)
	stmts = append(stmts,
		&ast.VarDeclStmt{Position: term.Position, Name: matchedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: seedAttempt.Matched},
		&ast.VarDeclStmt{Position: term.Position, Name: committedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
	)
	stmts = append(stmts, markCommittedStmts(committedName, term.Position, seedAttempt.Committed)...)
	stmts = append(stmts,
		&ast.VarDeclStmt{Position: term.Position, Name: leftName, Mutable: true, Type: leftType, Value: seedAttempt.Value},
		&ast.VarDeclStmt{Position: term.Position, Name: stopName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.WhileStmt{
			Position: term.Position,
			Cond:     &ast.UnaryExpr{Position: term.Position, Op: lexer.TOKEN_NOT, Operand: &ast.Ident{Position: term.Position, Name: stopName}},
			Body: append([]ast.Stmt{
				&ast.VarDeclStmt{Position: term.Position, Name: snapshotName, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
			}, ctx.lowerPostfixArms(term.Arms, snapshotName, stopName, matchedName, committedName, leftName)...),
		},
	)
	return loweredAttempt{Stmts: stmts, Matched: &ast.Ident{Position: term.Position, Name: matchedName}, Committed: &ast.Ident{Position: term.Position, Name: committedName}, Value: &ast.Ident{Position: term.Position, Name: leftName}}
}
func (ctx *statefulLowerContext) lowerNamedPrecedenceAttempt(term *ast.GrammarPrecedenceTerm) loweredAttempt {
	levels := make(map[string]ast.GrammarPrecedenceLevel, len(term.Levels))
	for _, level := range term.Levels {
		levels[level.Name] = level
	}
	top, ok := levels[term.Result]
	if !ok {
		return loweredAttempt{Matched: &ast.BoolLit{Position: term.Position, Value: false}, Committed: &ast.BoolLit{Position: term.Position, Value: false}, Value: zeroedCastExpr(term.Position, grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType))}
	}
	return ctx.lowerPrecedenceLevelAttempt(top, levels)
}
func (ctx *statefulLowerContext) lowerPrecedenceLevelAttempt(level ast.GrammarPrecedenceLevel, levels map[string]ast.GrammarPrecedenceLevel) loweredAttempt {
	seedAttempt := ctx.lowerPrecedenceLevelOperandAttempt(level.Seed, levels)
	leftType := ctx.inferPrecedenceOperandType(level.Seed, levels)
	if leftType == nil {
		leftType = grammarResolvedValueTypeExpr(level.Position, ctx.production.ReturnType)
	}
	stopName := ctx.fresh("precedence_stop")
	matchedName := ctx.fresh("precedence_matched")
	committedName := ctx.fresh("precedence_committed")
	snapshotName := ctx.fresh("precedence_cursor")
	leftName := level.LeftName
	stmts := append([]ast.Stmt{}, seedAttempt.Stmts...)
	stmts = append(stmts,
		&ast.VarDeclStmt{Position: level.Position, Name: matchedName, Mutable: true, Type: builtinTypeExpr(level.Position, "bool"), Value: seedAttempt.Matched},
		&ast.VarDeclStmt{Position: level.Position, Name: committedName, Mutable: true, Type: builtinTypeExpr(level.Position, "bool"), Value: &ast.BoolLit{Position: level.Position, Value: false}},
	)
	stmts = append(stmts, markCommittedStmts(committedName, level.Position, seedAttempt.Committed)...)
	stmts = append(stmts,
		&ast.VarDeclStmt{Position: level.Position, Name: leftName, Mutable: true, Type: leftType, Value: seedAttempt.Value},
		&ast.VarDeclStmt{Position: level.Position, Name: stopName, Mutable: true, Type: builtinTypeExpr(level.Position, "bool"), Value: &ast.BoolLit{Position: level.Position, Value: false}},
		&ast.WhileStmt{
			Position: level.Position,
			Cond:     &ast.UnaryExpr{Position: level.Position, Op: lexer.TOKEN_NOT, Operand: &ast.Ident{Position: level.Position, Name: stopName}},
			Body: append([]ast.Stmt{
				&ast.VarDeclStmt{Position: level.Position, Name: snapshotName, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, level.Position)},
			}, ctx.lowerPrecedenceLevelArms(level.Arms, snapshotName, stopName, matchedName, committedName, leftName, levels)...),
		},
	)
	return loweredAttempt{Stmts: stmts, Matched: &ast.Ident{Position: level.Position, Name: matchedName}, Committed: &ast.Ident{Position: level.Position, Name: committedName}, Value: &ast.Ident{Position: level.Position, Name: leftName}}
}
func (ctx *statefulLowerContext) lowerPrecedenceLevelArms(arms []ast.GrammarPrecedenceArm, snapshotName string, stopName string, matchedName string, committedName string, leftName string, levels map[string]ast.GrammarPrecedenceLevel) []ast.Stmt {
	if len(arms) == 0 {
		return []ast.Stmt{
			restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, ctx.production.Position),
			&ast.AssignStmt{Position: ctx.production.Position, Target: &ast.Ident{Position: ctx.production.Position, Name: stopName}, Value: &ast.BoolLit{Position: ctx.production.Position, Value: true}},
		}
	}
	arm := arms[0]
	opAttempt := ctx.lowerPrecedenceLevelOperandAttempt(arm.Op, levels)
	thenBranch := make([]ast.Stmt, 0, len(arm.Bindings)+1)
	thenBranch = append(thenBranch, markCommittedStmts(committedName, arm.Position, opAttempt.Committed)...)
	if arm.OpName != "" {
		thenBranch = append(thenBranch, &ast.VarDeclStmt{Position: arm.Position, Name: arm.OpName, Value: opAttempt.Value})
	}
	for _, binding := range arm.Bindings {
		bindAttempt := ctx.lowerPrecedenceLevelOperandAttempt(binding.Term, levels)
		thenBranch = append(thenBranch, bindAttempt.Stmts...)
		thenBranch = append(thenBranch, markCommittedStmts(committedName, binding.Position, bindAttempt.Committed)...)
		if ctx.precedenceOperandCanFail(binding.Term, levels) {
			thenBranch = append(thenBranch, ctx.failureGuard(binding.Term.Pos(), snapshotName, bindAttempt.Matched)...)
		}
		thenBranch = append(thenBranch, &ast.VarDeclStmt{Position: binding.Position, Name: binding.Name, Value: bindAttempt.Value})
	}
	thenBranch = append(thenBranch, &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: leftName}, Value: arm.Value})
	fallback := []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, arm.Position)}
	if len(arms) > 1 {
		fallback = append(fallback, ctx.lowerPrecedenceLevelArms(arms[1:], snapshotName, stopName, matchedName, committedName, leftName, levels)...)
	} else {
		fallback = append(fallback, &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: stopName}, Value: &ast.BoolLit{Position: arm.Position, Value: true}})
	}
	result := append([]ast.Stmt{}, opAttempt.Stmts...)
	result = append(result, &ast.IfStmt{Position: arm.Position, Cond: opAttempt.Matched, Then: thenBranch, Else: []ast.Stmt{&ast.IfStmt{Position: arm.Position, Cond: opAttempt.Committed, Then: []ast.Stmt{committedAssignTrueStmt(committedName, arm.Position), &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: matchedName}, Value: &ast.BoolLit{Position: arm.Position, Value: false}}, &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: stopName}, Value: &ast.BoolLit{Position: arm.Position, Value: true}}}, Else: fallback}}})
	return result
}
func (ctx *statefulLowerContext) resolvePrecedenceLevelCall(term *ast.GrammarCallTerm, levels map[string]ast.GrammarPrecedenceLevel) (ast.GrammarPrecedenceLevel, bool) {
	if term == nil || !term.Explicit || len(term.Args) != 0 {
		return ast.GrammarPrecedenceLevel{}, false
	}
	level, ok := levels[term.Name]
	if !ok {
		return ast.GrammarPrecedenceLevel{}, false
	}
	return level, true
}
func (ctx *statefulLowerContext) lowerPrecedenceLevelOperandAttempt(term ast.GrammarTerm, levels map[string]ast.GrammarPrecedenceLevel) loweredAttempt {
	if call, ok := term.(*ast.GrammarCallTerm); ok {
		if level, ok := ctx.resolvePrecedenceLevelCall(call, levels); ok {
			return ctx.lowerPrecedenceLevelAttempt(level, levels)
		}
	}
	return ctx.lowerAttempt(term)
}
func (ctx *statefulLowerContext) precedenceOperandCanFail(term ast.GrammarTerm, levels map[string]ast.GrammarPrecedenceLevel) bool {
	if call, ok := term.(*ast.GrammarCallTerm); ok {
		if level, ok := ctx.resolvePrecedenceLevelCall(call, levels); ok {
			return ctx.precedenceOperandCanFail(level.Seed, levels)
		}
	}
	return ctx.termCanFail(term)
}
func (ctx *statefulLowerContext) inferPrecedenceOperandType(term ast.GrammarTerm, levels map[string]ast.GrammarPrecedenceLevel) ast.TypeExpr {
	if call, ok := term.(*ast.GrammarCallTerm); ok {
		if _, ok := ctx.resolvePrecedenceLevelCall(call, levels); ok {
			return grammarResolvedValueTypeExpr(call.Position, ctx.production.ReturnType)
		}
	}
	return ctx.inferTermType(term)
}
func (ctx *statefulLowerContext) lowerPrecedenceArms(arms []ast.GrammarPrecedenceArm, snapshotName string, stopName string, matchedName string, committedName string, leftName string, assoc string) []ast.Stmt {
	if len(arms) == 0 {
		return []ast.Stmt{
			restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, ctx.production.Position),
			&ast.AssignStmt{Position: ctx.production.Position, Target: &ast.Ident{Position: ctx.production.Position, Name: stopName}, Value: &ast.BoolLit{Position: ctx.production.Position, Value: true}},
		}
	}
	arm := arms[0]
	opAttempt := ctx.lowerAttempt(arm.Op)
	thenBranch := make([]ast.Stmt, 0, len(arm.Bindings)+1)
	thenBranch = append(thenBranch, markCommittedStmts(committedName, arm.Position, opAttempt.Committed)...)
	if arm.OpName != "" {
		thenBranch = append(thenBranch, &ast.VarDeclStmt{Position: arm.Position, Name: arm.OpName, Value: opAttempt.Value})
	}
	for _, binding := range arm.Bindings {
		facts := ctx.termFacts(binding.Term)
		bindAttempt := ctx.lowerAttempt(binding.Term)
		thenBranch = append(thenBranch, bindAttempt.Stmts...)
		thenBranch = append(thenBranch, markCommittedStmts(committedName, binding.Position, bindAttempt.Committed)...)
		if facts.CanFail {
			thenBranch = append(thenBranch, ctx.failureGuard(binding.Term.Pos(), snapshotName, bindAttempt.Matched)...)
		}
		thenBranch = append(thenBranch, &ast.VarDeclStmt{Position: binding.Position, Name: binding.Name, Value: bindAttempt.Value})
	}
	thenBranch = append(thenBranch, &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: leftName}, Value: arm.Value})
	if assoc == ast.GrammarAssociativityRight || assoc == ast.GrammarAssociativityNonAssoc {
		thenBranch = append(thenBranch, &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: stopName}, Value: &ast.BoolLit{Position: arm.Position, Value: true}})
	}
	fallback := []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, arm.Position)}
	if len(arms) > 1 {
		fallback = append(fallback, ctx.lowerPrecedenceArms(arms[1:], snapshotName, stopName, matchedName, committedName, leftName, assoc)...)
	} else {
		fallback = append(fallback, &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: stopName}, Value: &ast.BoolLit{Position: arm.Position, Value: true}})
	}
	result := append([]ast.Stmt{}, opAttempt.Stmts...)
	result = append(result, &ast.IfStmt{Position: arm.Position, Cond: opAttempt.Matched, Then: thenBranch, Else: []ast.Stmt{&ast.IfStmt{Position: arm.Position, Cond: opAttempt.Committed, Then: []ast.Stmt{committedAssignTrueStmt(committedName, arm.Position), &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: matchedName}, Value: &ast.BoolLit{Position: arm.Position, Value: false}}, &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: stopName}, Value: &ast.BoolLit{Position: arm.Position, Value: true}}}, Else: fallback}}})
	return result
}
func (ctx *statefulLowerContext) lowerPostfixArms(arms []ast.GrammarPostfixArm, snapshotName string, stopName string, matchedName string, committedName string, leftName string) []ast.Stmt {
	if len(arms) == 0 {
		return []ast.Stmt{
			restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, ctx.production.Position),
			&ast.AssignStmt{Position: ctx.production.Position, Target: &ast.Ident{Position: ctx.production.Position, Name: stopName}, Value: &ast.BoolLit{Position: ctx.production.Position, Value: true}},
		}
	}
	arm := arms[0]
	opAttempt := ctx.lowerAttempt(arm.Op)
	thenBranch := make([]ast.Stmt, 0, len(arm.Bindings)+1)
	thenBranch = append(thenBranch, markCommittedStmts(committedName, arm.Position, opAttempt.Committed)...)
	if arm.OpName != "" {
		thenBranch = append(thenBranch, &ast.VarDeclStmt{Position: arm.Position, Name: arm.OpName, Value: opAttempt.Value})
	}
	for _, binding := range arm.Bindings {
		facts := ctx.termFacts(binding.Term)
		bindAttempt := ctx.lowerAttempt(binding.Term)
		thenBranch = append(thenBranch, bindAttempt.Stmts...)
		thenBranch = append(thenBranch, markCommittedStmts(committedName, binding.Position, bindAttempt.Committed)...)
		if facts.CanFail {
			thenBranch = append(thenBranch, ctx.failureGuard(binding.Term.Pos(), snapshotName, bindAttempt.Matched)...)
		}
		thenBranch = append(thenBranch, &ast.VarDeclStmt{Position: binding.Position, Name: binding.Name, Value: bindAttempt.Value})
	}
	thenBranch = append(thenBranch, &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: leftName}, Value: arm.Value})
	fallback := []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, arm.Position)}
	if len(arms) > 1 {
		fallback = append(fallback, ctx.lowerPostfixArms(arms[1:], snapshotName, stopName, matchedName, committedName, leftName)...)
	} else {
		fallback = append(fallback, &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: stopName}, Value: &ast.BoolLit{Position: arm.Position, Value: true}})
	}
	result := append([]ast.Stmt{}, opAttempt.Stmts...)
	result = append(result, &ast.IfStmt{Position: arm.Position, Cond: opAttempt.Matched, Then: thenBranch, Else: []ast.Stmt{&ast.IfStmt{Position: arm.Position, Cond: opAttempt.Committed, Then: []ast.Stmt{committedAssignTrueStmt(committedName, arm.Position), &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: matchedName}, Value: &ast.BoolLit{Position: arm.Position, Value: false}}, &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: stopName}, Value: &ast.BoolLit{Position: arm.Position, Value: true}}}, Else: fallback}}})
	return result
}
func (ctx *statefulLowerContext) lowerListAttempt(term *ast.GrammarListTerm) loweredAttempt {
	elemType := ctx.termFacts(term.Elem).ValueType
	resultType := listTypeExpr(term.Position, elemType)
	resultName := ctx.fresh("list_value")
	stopName := ctx.fresh("list_stop")
	matchedName := ctx.fresh("list_matched")
	committedName := ctx.fresh("list_committed")
	itemSnapshot := ctx.fresh("item_cursor")
	itemAttempt := ctx.lowerAttempt(term.Elem)
	loop := &ast.WhileStmt{
		Position: term.Position,
		Cond:     &ast.UnaryExpr{Position: term.Position, Op: lexer.TOKEN_NOT, Operand: &ast.Ident{Position: term.Position, Name: stopName}},
		Body:     ctx.lowerListLoopBody(term, itemSnapshot, resultName, stopName, matchedName, committedName, itemAttempt),
	}
	loopStmt := ast.Stmt(loop)
	resultInit := ast.Expr(&ast.ListLitExpr{Position: term.Position})
	if ctx.allocExpr != nil {
		resultInit = zeroedCastExpr(term.Position, resultType)
		loopStmt = &ast.InStoreStmt{
			Position: term.Position,
			Store:    ctx.allocExpr,
			Body: []ast.Stmt{
				&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: resultName}, Value: &ast.ListLitExpr{Position: term.Position}},
				loop,
			},
		}
	}
	body := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: matchedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: true}},
		&ast.VarDeclStmt{Position: term.Position, Name: committedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.VarDeclStmt{Position: term.Position, Name: resultName, Mutable: true, Type: resultType, Value: resultInit},
		&ast.VarDeclStmt{Position: term.Position, Name: stopName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		loopStmt,
	}
	return loweredAttempt{Stmts: body, Matched: &ast.Ident{Position: term.Position, Name: matchedName}, Committed: &ast.Ident{Position: term.Position, Name: committedName}, Value: &ast.Ident{Position: term.Position, Name: resultName}}
}
func (ctx *statefulLowerContext) lowerListLoopBody(term *ast.GrammarListTerm, itemSnapshot string, resultName string, stopName string, matchedName string, committedName string, itemAttempt loweredAttempt) []ast.Stmt {
	continueBody := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: itemSnapshot, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
	}
	continueBody = append(continueBody, itemAttempt.Stmts...)
	continueBody = append(continueBody, markCommittedStmts(committedName, term.Position, itemAttempt.Committed)...)
	if term.Separator == nil {
		continueBody = append(continueBody, &ast.IfStmt{
			Position: term.Position,
			Cond:     itemAttempt.Matched,
			Then: []ast.Stmt{
				listPushStmt(term.Position, resultName, itemAttempt.Value),
			},
			Else: []ast.Stmt{&ast.IfStmt{Position: term.Position, Cond: itemAttempt.Committed, Then: []ast.Stmt{&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: matchedName}, Value: &ast.BoolLit{Position: term.Position, Value: false}}, &ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: stopName}, Value: &ast.BoolLit{Position: term.Position, Value: true}}}, Else: []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, itemSnapshot, term.Position), &ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: stopName}, Value: &ast.BoolLit{Position: term.Position, Value: true}}}}},
		})
		if stopCond := ctx.lowerListUntilMatchExpr(term.Position, term.Until); stopCond != nil {
			return []ast.Stmt{&ast.IfStmt{
				Position: term.Position,
				Cond:     stopCond,
				Then: []ast.Stmt{
					&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: stopName}, Value: &ast.BoolLit{Position: term.Position, Value: true}},
				},
				Else: continueBody,
			}}
		}
		return continueBody
	}
	sepSnapshot := ctx.fresh("sep_cursor")
	sepAttempt := ctx.lowerAttempt(term.Separator)
	continueBody = append(continueBody, &ast.IfStmt{
		Position: term.Position,
		Cond:     itemAttempt.Matched,
		Then: append([]ast.Stmt{
			listPushStmt(term.Position, resultName, itemAttempt.Value),
			&ast.VarDeclStmt{Position: term.Position, Name: sepSnapshot, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
		}, append(append(sepAttempt.Stmts, markCommittedStmts(committedName, term.Position, sepAttempt.Committed)...), &ast.IfStmt{
			Position: term.Position,
			Cond:     &ast.UnaryExpr{Position: term.Position, Op: lexer.TOKEN_NOT, Operand: sepAttempt.Matched},
			Then:     []ast.Stmt{&ast.IfStmt{Position: term.Position, Cond: sepAttempt.Committed, Then: []ast.Stmt{&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: matchedName}, Value: &ast.BoolLit{Position: term.Position, Value: false}}, &ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: stopName}, Value: &ast.BoolLit{Position: term.Position, Value: true}}}, Else: []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, sepSnapshot, term.Position), &ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: stopName}, Value: &ast.BoolLit{Position: term.Position, Value: true}}}}},
		})...),
		Else: []ast.Stmt{&ast.IfStmt{Position: term.Position, Cond: itemAttempt.Committed, Then: []ast.Stmt{&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: matchedName}, Value: &ast.BoolLit{Position: term.Position, Value: false}}, &ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: stopName}, Value: &ast.BoolLit{Position: term.Position, Value: true}}}, Else: []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, itemSnapshot, term.Position), &ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: stopName}, Value: &ast.BoolLit{Position: term.Position, Value: true}}}}},
	})
	if stopCond := ctx.lowerListUntilMatchExpr(term.Position, term.Until); stopCond != nil {
		return []ast.Stmt{&ast.IfStmt{
			Position: term.Position,
			Cond:     stopCond,
			Then: []ast.Stmt{
				&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: stopName}, Value: &ast.BoolLit{Position: term.Position, Value: true}},
			},
			Else: continueBody,
		}}
	}
	return continueBody
}
func (ctx *statefulLowerContext) lowerListUntilMatchExpr(pos lexer.Pos, stops []ast.GrammarTerm) ast.Expr {
	if len(stops) == 0 || ctx.tokenReceiver == "" {
		return nil
	}
	tokenExpr := ctx.currentTokenExpr(pos)
	var combined ast.Expr
	for _, stop := range stops {
		stopExpr := ctx.lowerListUntilStopExpr(pos, tokenExpr, stop)
		if stopExpr == nil {
			continue
		}
		if combined == nil {
			combined = stopExpr
			continue
		}
		combined = &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_OR, Left: combined, Right: stopExpr}
	}
	return combined
}
func (ctx *statefulLowerContext) lowerListUntilStopExpr(pos lexer.Pos, tokenExpr ast.Expr, stop ast.GrammarTerm) ast.Expr {
	switch n := stop.(type) {
	case *ast.GrammarTokenTerm:
		return grammarTokenMatchExpr(pos, tokenExpr, ctx.tokenKindField, ctx.tokenLookupFunc, n.Value)
	case *ast.GrammarTokenKindTerm:
		return grammarTokenKindMatchExpr(pos, tokenExpr, ctx.tokenKindField, grammarTokenKindExpr(n.Position, ctx.tokenKindType, n.Kind))
	case *ast.GrammarCallTerm:
		if kindExpr, ok := grammarTokenKindMatcher(n); ok {
			return grammarTokenKindMatchExpr(pos, tokenExpr, ctx.tokenKindField, kindExpr)
		}
	case *ast.GrammarTokenSetRefTerm:
		return nil
	}
	return nil
}
