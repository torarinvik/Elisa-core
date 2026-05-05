package grammar

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (ctx *statefulLowerContext) lowerAttempt(term ast.GrammarTerm) loweredAttempt {
	switch n := term.(type) {
	case *ast.GrammarTokenTerm:
		valueName := ctx.fresh("token")
		matchedName := ctx.fresh("matched")
		valueIdent := &ast.Ident{Position: n.Position, Name: valueName}
		return loweredAttempt{
			Stmts: []ast.Stmt{
				&ast.VarDeclStmt{Position: n.Position, Name: valueName, Value: lowerTermExpr(ctx.lowerExprContext(), n)},
				&ast.VarDeclStmt{Position: n.Position, Name: matchedName, Value: grammarTokenMatchExpr(n.Position, valueIdent, ctx.tokenKindField, ctx.tokenLookupFunc, n.Value)},
			},
			Matched:   &ast.Ident{Position: n.Position, Name: matchedName},
			Committed: &ast.BoolLit{Position: n.Position, Value: false},
			Value:     valueIdent,
		}
	case *ast.GrammarTokenKindTerm:
		valueName := ctx.fresh("token")
		matchedName := ctx.fresh("matched")
		valueIdent := &ast.Ident{Position: n.Position, Name: valueName}
		kindExpr := grammarTokenKindExpr(n.Position, ctx.tokenKindType, n.Kind)
		return loweredAttempt{
			Stmts: []ast.Stmt{
				&ast.VarDeclStmt{Position: n.Position, Name: valueName, Value: lowerTermExpr(ctx.lowerExprContext(), n)},
				&ast.VarDeclStmt{Position: n.Position, Name: matchedName, Value: grammarTokenKindMatchExpr(n.Position, valueIdent, ctx.tokenKindField, kindExpr)},
			},
			Matched:   &ast.Ident{Position: n.Position, Name: matchedName},
			Committed: &ast.BoolLit{Position: n.Position, Value: false},
			Value:     valueIdent,
		}
	case *ast.GrammarCallTerm:
		if kindExpr, ok := grammarTokenKindMatcher(n); ok {
			valueName := ctx.fresh("token")
			matchedName := ctx.fresh("matched")
			valueIdent := &ast.Ident{Position: n.Position, Name: valueName}
			return loweredAttempt{
				Stmts: []ast.Stmt{
					&ast.VarDeclStmt{Position: n.Position, Name: valueName, Value: lowerTermExpr(ctx.lowerExprContext(), n)},
					&ast.VarDeclStmt{Position: n.Position, Name: matchedName, Value: grammarTokenKindMatchExpr(n.Position, valueIdent, ctx.tokenKindField, kindExpr)},
				},
				Matched:   &ast.Ident{Position: n.Position, Name: matchedName},
				Committed: &ast.BoolLit{Position: n.Position, Value: false},
				Value:     valueIdent,
			}
		}
		if _, production, ok := ctx.resolveGrammarProductionInfo(n); ok && production.RecoverMsg != nil && len(production.RecoverUntil) != 0 {
			valueExpr := lowerTermExpr(ctx.lowerExprContext(), n)
			if callName, args, ok := ctx.resolveGrammarRecoveredPublicCall(n); ok {
				valueExpr = &ast.CallExpr{Position: n.Position, Func: &ast.Ident{Position: n.Position, Name: callName}, Args: args}
			}
			if production.ReturnType == nil {
				return loweredAttempt{
					Stmts:     []ast.Stmt{&ast.ExprStmt{Position: n.Position, Expr: valueExpr}},
					Matched:   &ast.BoolLit{Position: n.Position, Value: true},
					Committed: &ast.BoolLit{Position: n.Position, Value: false},
					Value:     &ast.BoolLit{Position: n.Position, Value: true},
				}
			}
			valueName := ctx.fresh("value")
			return loweredAttempt{
				Stmts:     []ast.Stmt{&ast.VarDeclStmt{Position: n.Position, Name: valueName, Value: valueExpr}},
				Matched:   &ast.BoolLit{Position: n.Position, Value: true},
				Committed: &ast.BoolLit{Position: n.Position, Value: false},
				Value:     &ast.Ident{Position: n.Position, Name: valueName},
			}
		}
		if tryName, args, ok := ctx.resolveGrammarProductionCall(n); ok {
			matchedName := ctx.fresh("matched")
			committedName := ctx.fresh("committed")
			valueName := ctx.fresh("value")
			_, production, _ := ctx.resolveGrammarProductionInfo(n)
			tryCall := ast.Expr(&ast.CallExpr{
				Position: n.Position,
				Func:     &ast.Ident{Position: n.Position, Name: tryName},
				Args:     args,
			})
			if grammarErrorTypeExpr(production.ReturnType) != nil {
				tryCall = grammarMaybeTryExpr(tryCall, ctx.production.ReturnType)
			}
			return loweredAttempt{
				Stmts: []ast.Stmt{
					&ast.TupleBindStmt{
						Position: n.Position,
						Names: []ast.TupleBindName{
							{Position: n.Position, Name: matchedName},
							{Position: n.Position, Name: committedName},
							{Position: n.Position, Name: valueName},
						},
						Declare: true,
						Value:   tryCall,
					},
				},
				Matched:   &ast.Ident{Position: n.Position, Name: matchedName},
				Committed: &ast.Ident{Position: n.Position, Name: committedName},
				Value:     &ast.Ident{Position: n.Position, Name: valueName},
			}
		}
		valueName := ctx.fresh("value")
		valueExpr := lowerTermExpr(ctx.lowerExprContext(), n)
		return loweredAttempt{
			Stmts:     []ast.Stmt{&ast.VarDeclStmt{Position: n.Position, Name: valueName, Value: valueExpr}},
			Matched:   &ast.BoolLit{Position: n.Position, Value: true},
			Committed: &ast.BoolLit{Position: n.Position, Value: false},
			Value:     &ast.Ident{Position: n.Position, Name: valueName},
		}
	case *ast.GrammarChoiceTerm:
		return ctx.lowerChoiceAttempt(n)
	case *ast.GrammarWhenTerm:
		return ctx.lowerWhenAttempt(n)
	case *ast.GrammarRequiredTerm:
		return ctx.lowerRequiredAttempt(n)
	case *ast.GrammarDelimitedTerm:
		return ctx.lowerDelimitedAttempt(n)
	case *ast.GrammarSeqTerm:
		return ctx.lowerSeqAttempt(n)
	case *ast.GrammarLookaheadTerm:
		return ctx.lowerLookaheadAttempt(n)
	case *ast.GrammarExprTerm:
		valueName := ctx.fresh("value")
		valueExpr := ast.Expr(n.Expr)
		valueType := n.Type
		if wrapped, ok := lowerAllocatingGrammarExpr(ctx.lowerExprContext(), n.Position, n.Type, n.Expr); ok {
			valueExpr = wrapped
			valueType = nil
		}
		return loweredAttempt{
			Stmts:     []ast.Stmt{&ast.VarDeclStmt{Position: n.Position, Name: valueName, Type: valueType, Value: valueExpr}},
			Matched:   &ast.BoolLit{Position: n.Position, Value: true},
			Committed: &ast.BoolLit{Position: n.Position, Value: false},
			Value:     &ast.Ident{Position: n.Position, Name: valueName},
		}
	case *ast.GrammarSingletonTerm:
		return ctx.lowerSingletonAttempt(n)
	case *ast.GrammarEmptyTerm:
		return ctx.lowerEmptyAttempt(n)
	case *ast.GrammarConcatTerm:
		return ctx.lowerConcatAttempt(n)
	case *ast.GrammarRecoverTerm:
		return ctx.lowerRecoveredAttempt(n)
	case *ast.GrammarGuardTerm:
		valueName := ctx.fresh("guard")
		guardIdent := &ast.Ident{Position: n.Position, Name: valueName}
		return loweredAttempt{
			Stmts:     []ast.Stmt{&ast.VarDeclStmt{Position: n.Position, Name: valueName, Value: n.Cond}},
			Matched:   guardIdent,
			Committed: &ast.BoolLit{Position: n.Position, Value: false},
			Value:     guardIdent,
		}
	case *ast.GrammarAttemptTerm:
		matchedName := ctx.fresh("matched")
		valueName := ctx.fresh("value")
		return loweredAttempt{
			Stmts: []ast.Stmt{
				&ast.TupleBindStmt{
					Position: n.Position,
					Names: []ast.TupleBindName{
						{Position: n.Position, Name: matchedName},
						{Position: n.Position, Name: valueName},
					},
					Declare: true,
					Value:   n.Expr,
				},
			},
			Matched:   &ast.Ident{Position: n.Position, Name: matchedName},
			Committed: &ast.BoolLit{Position: n.Position, Value: false},
			Value:     &ast.Ident{Position: n.Position, Name: valueName},
		}
	case *ast.GrammarCutTerm:
		return loweredAttempt{Matched: &ast.BoolLit{Position: n.Position, Value: true}, Committed: &ast.BoolLit{Position: n.Position, Value: true}, Value: &ast.BoolLit{Position: n.Position, Value: true}}
	case *ast.GrammarOptionalTerm:
		return ctx.lowerOptionalAttempt(n)
	case *ast.GrammarListTerm:
		return ctx.lowerListAttempt(n)
	case *ast.GrammarRepeatTerm:
		return ctx.lowerListAttempt(repeatTermAsList(n))
	case *ast.GrammarFlatRepeatTerm:
		return ctx.lowerFlatRepeatAttempt(n)
	case *ast.GrammarWhileTerm:
		return ctx.lowerFlatRepeatAttempt(&ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: n.Elem, Until: n.Until})
	case *ast.GrammarSeparatedTerm:
		return ctx.lowerListAttempt(separatedTermAsList(n))
	case *ast.GrammarSuffixTerm:
		return ctx.lowerSuffixAttempt(n)
	case *ast.GrammarPostfixTerm:
		return ctx.lowerPostfixAttempt(n)
	case *ast.GrammarPrecedenceTerm:
		return ctx.lowerPrecedenceAttempt(n)
	case *ast.GrammarPassTerm:
		return loweredAttempt{Matched: &ast.BoolLit{Position: n.Position, Value: true}, Committed: &ast.BoolLit{Position: n.Position, Value: false}, Value: zeroedCastExpr(n.Position, grammarResolvedValueTypeExpr(n.Position, ctx.production.ReturnType))}
	case *ast.GrammarBindTerm:
		return ctx.lowerAttempt(n.Term)
	default:
		return loweredAttempt{Matched: &ast.BoolLit{Position: term.Pos(), Value: true}, Committed: &ast.BoolLit{Position: term.Pos(), Value: false}, Value: &ast.ZeroedLit{Position: term.Pos()}}
	}
}
func (ctx *statefulLowerContext) lowerWhenAttempt(term *ast.GrammarWhenTerm) loweredAttempt {
	termType := ctx.termValueTypeOrProduction(term.Position, term)
	thenTerm := grammarSpecializeUntypedEmptyTerm(term.Then, termType)
	elseTerm := grammarSpecializeUntypedEmptyTerm(term.Else, termType)
	thenAttempt := ctx.lowerAttempt(thenTerm)
	elseAttempt := ctx.lowerAttempt(elseTerm)
	thenFacts := ctx.termFacts(thenTerm)
	elseFacts := ctx.termFacts(elseTerm)
	condName := ctx.fresh("when_cond")
	matchedName := ctx.fresh("when_matched")
	committedName := ctx.fresh("when_committed")
	valueName := ctx.fresh("when_value")
	valueIdent := &ast.Ident{Position: term.Position, Name: valueName}
	thenValue := grammarCoerceValueToType(term.Position, thenAttempt.Value, thenFacts.ValueType, termType)
	elseValue := grammarCoerceValueToType(term.Position, elseAttempt.Value, elseFacts.ValueType, termType)
	thenBranch := append([]ast.Stmt{}, thenAttempt.Stmts...)
	thenBranch = append(thenBranch, markCommittedStmts(committedName, term.Position, thenAttempt.Committed)...)
	thenBranch = append(thenBranch,
		&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: matchedName}, Value: thenAttempt.Matched},
		&ast.AssignStmt{Position: term.Position, Target: valueIdent, Value: thenValue},
	)
	elseBranch := append([]ast.Stmt{}, elseAttempt.Stmts...)
	elseBranch = append(elseBranch, markCommittedStmts(committedName, term.Position, elseAttempt.Committed)...)
	elseBranch = append(elseBranch,
		&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: matchedName}, Value: elseAttempt.Matched},
		&ast.AssignStmt{Position: term.Position, Target: valueIdent, Value: elseValue},
	)
	return loweredAttempt{
		Stmts: []ast.Stmt{
			&ast.VarDeclStmt{Position: term.Position, Name: condName, Value: ctx.grammarWhenCondExpr(term)},
			&ast.VarDeclStmt{Position: term.Position, Name: matchedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
			&ast.VarDeclStmt{Position: term.Position, Name: committedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
			&ast.VarDeclStmt{Position: term.Position, Name: valueName, Mutable: true, Type: termType, Value: zeroedCastExpr(term.Position, termType)},
			&ast.IfStmt{Position: term.Position, Cond: &ast.Ident{Position: term.Position, Name: condName}, Then: thenBranch, Else: elseBranch},
		},
		Matched:   &ast.Ident{Position: term.Position, Name: matchedName},
		Committed: &ast.Ident{Position: term.Position, Name: committedName},
		Value:     valueIdent,
	}
}
func grammarSpecializeUntypedEmptyTerm(term ast.GrammarTerm, valueType ast.TypeExpr) ast.GrammarTerm {
	empty, ok := term.(*ast.GrammarEmptyTerm)
	if !ok || empty == nil || empty.Type != nil {
		return term
	}
	return &ast.GrammarEmptyTerm{Position: empty.Position, Type: grammarListElementType(empty.Position, nil, valueType)}
}
func (ctx *statefulLowerContext) grammarWhenCondExpr(term *ast.GrammarWhenTerm) ast.Expr {
	if term.TokenKindGate == "" {
		return term.Cond
	}
	return grammarTokenKindMatchExpr(
		term.Position,
		ctx.currentTokenExpr(term.Position),
		ctx.tokenKindField,
		grammarTokenKindExpr(term.Position, ctx.tokenKindType, term.TokenKindGate),
	)
}
func (ctx *statefulLowerContext) lowerDelimitedAttempt(term *ast.GrammarDelimitedTerm) loweredAttempt {
	if term == nil || ctx.cursorReceiver == "" || ctx.cursorField == "" {
		if term == nil {
			return loweredAttempt{Matched: &ast.BoolLit{Position: ctx.production.Position, Value: false}, Committed: &ast.BoolLit{Position: ctx.production.Position, Value: false}, Value: &ast.ZeroedLit{Position: ctx.production.Position}}
		}
		return ctx.lowerAttempt(term.Body)
	}
	openAttempt := ctx.lowerAttempt(term.Open)
	bodyAttempt := ctx.lowerAttempt(term.Body)
	closeAttempt := ctx.lowerRequiredAttempt(&ast.GrammarRequiredTerm{Position: term.Position, Term: term.Close, Message: term.Message})
	termType := ctx.termValueTypeOrProduction(term.Position, term.Body)
	bodyValue := bodyAttempt.Value
	if bodyValue == nil {
		bodyValue = zeroedCastExpr(term.Position, termType)
	}
	snapshotName := ctx.fresh("delimited_cursor")
	matchedName := ctx.fresh("delimited_matched")
	committedName := ctx.fresh("delimited_committed")
	valueName := ctx.fresh("delimited_value")
	valueIdent := &ast.Ident{Position: term.Position, Name: valueName}
	closeSuccess := append([]ast.Stmt{}, closeAttempt.Stmts...)
	closeSuccess = append(closeSuccess, markCommittedStmts(committedName, term.Position, closeAttempt.Committed)...)
	closeSuccess = append(closeSuccess, &ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: matchedName}, Value: &ast.BoolLit{Position: term.Position, Value: true}})
	bodySuccess := append([]ast.Stmt{}, bodyAttempt.Stmts...)
	bodySuccess = append(bodySuccess, markCommittedStmts(committedName, term.Position, bodyAttempt.Committed)...)
	bodySuccess = append(bodySuccess,
		&ast.AssignStmt{Position: term.Position, Target: valueIdent, Value: bodyValue},
		&ast.IfStmt{Position: term.Position, Cond: bodyAttempt.Matched, Then: closeSuccess, Else: []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, term.Position)}},
	)
	stms := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: snapshotName, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
		&ast.VarDeclStmt{Position: term.Position, Name: matchedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.VarDeclStmt{Position: term.Position, Name: committedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.VarDeclStmt{Position: term.Position, Name: valueName, Mutable: true, Type: termType, Value: zeroedCastExpr(term.Position, termType)},
	}
	stms = append(stms, openAttempt.Stmts...)
	stms = append(stms, markCommittedStmts(committedName, term.Position, openAttempt.Committed)...)
	stms = append(stms, &ast.IfStmt{Position: term.Position, Cond: openAttempt.Matched, Then: bodySuccess, Else: []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, term.Position)}})
	return loweredAttempt{Stmts: stms, Matched: &ast.Ident{Position: term.Position, Name: matchedName}, Committed: &ast.Ident{Position: term.Position, Name: committedName}, Value: valueIdent}
}
func (ctx *statefulLowerContext) lowerSeqAttempt(term *ast.GrammarSeqTerm) loweredAttempt {
	if term == nil || len(term.Terms) == 0 || ctx.cursorReceiver == "" || ctx.cursorField == "" {
		pos := ctx.production.Position
		if term != nil {
			pos = term.Position
			if len(term.Terms) != 0 {
				return ctx.lowerAttempt(term.Terms[len(term.Terms)-1])
			}
		}
		return loweredAttempt{Matched: &ast.BoolLit{Position: pos, Value: true}, Committed: &ast.BoolLit{Position: pos, Value: false}, Value: zeroedCastExpr(pos, grammarResolvedValueTypeExpr(pos, ctx.production.ReturnType))}
	}
	termType := ctx.termValueTypeOrProduction(term.Position, term)
	snapshotName := ctx.fresh("seq_cursor")
	matchedName := ctx.fresh("seq_matched")
	committedName := ctx.fresh("seq_committed")
	valueName := ctx.fresh("seq_value")
	valueIdent := &ast.Ident{Position: term.Position, Name: valueName}
	stms := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: snapshotName, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
		&ast.VarDeclStmt{Position: term.Position, Name: matchedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.VarDeclStmt{Position: term.Position, Name: committedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.VarDeclStmt{Position: term.Position, Name: valueName, Mutable: true, Type: termType, Value: zeroedCastExpr(term.Position, termType)},
	}
	seqCtx := *ctx
	seqCtx.committedName = committedName
	stms = append(stms, seqCtx.lowerSeqTerms(term.Terms, snapshotName, matchedName, valueIdent)...)
	ctx.tempCounter = seqCtx.tempCounter
	return loweredAttempt{Stmts: stms, Matched: &ast.Ident{Position: term.Position, Name: matchedName}, Committed: &ast.Ident{Position: term.Position, Name: committedName}, Value: valueIdent}
}
func (ctx *statefulLowerContext) lowerLookaheadAttempt(term *ast.GrammarLookaheadTerm) loweredAttempt {
	if term == nil || ctx.cursorReceiver == "" || ctx.cursorField == "" {
		if term == nil {
			return loweredAttempt{Matched: &ast.BoolLit{Position: ctx.production.Position, Value: false}, Committed: &ast.BoolLit{Position: ctx.production.Position, Value: false}, Value: &ast.ZeroedLit{Position: ctx.production.Position}}
		}
		return ctx.lowerAttempt(term.Term)
	}
	inner := ctx.lowerAttempt(term.Term)
	termType := ctx.termValueTypeOrProduction(term.Position, term.Term)
	value := inner.Value
	if value == nil {
		value = zeroedCastExpr(term.Position, termType)
	}
	snapshotName := ctx.fresh("lookahead_cursor")
	matchedName := ctx.fresh("lookahead_matched")
	valueName := ctx.fresh("lookahead_value")
	valueIdent := &ast.Ident{Position: term.Position, Name: valueName}
	stms := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: snapshotName, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
		&ast.VarDeclStmt{Position: term.Position, Name: matchedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.VarDeclStmt{Position: term.Position, Name: valueName, Mutable: true, Type: termType, Value: zeroedCastExpr(term.Position, termType)},
	}
	stms = append(stms, inner.Stmts...)
	thenBranch := []ast.Stmt{
		&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: matchedName}, Value: inner.Matched},
		&ast.AssignStmt{Position: term.Position, Target: valueIdent, Value: value},
	}
	stms = append(stms,
		&ast.IfStmt{Position: term.Position, Cond: inner.Matched, Then: thenBranch},
		restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, term.Position),
	)
	return loweredAttempt{Stmts: stms, Matched: &ast.Ident{Position: term.Position, Name: matchedName}, Committed: &ast.BoolLit{Position: term.Position, Value: false}, Value: valueIdent}
}
func (ctx *statefulLowerContext) lowerSeqTerms(terms []ast.GrammarTerm, snapshotName string, matchedName string, valueTarget ast.Expr) []ast.Stmt {
	if len(terms) == 0 {
		return []ast.Stmt{&ast.AssignStmt{Position: ctx.production.Position, Target: &ast.Ident{Position: ctx.production.Position, Name: matchedName}, Value: &ast.BoolLit{Position: ctx.production.Position, Value: true}}}
	}
	current := terms[0]
	if pass, ok := current.(*ast.GrammarPassTerm); ok {
		if len(terms) == 1 {
			return []ast.Stmt{&ast.AssignStmt{Position: pass.Position, Target: &ast.Ident{Position: pass.Position, Name: matchedName}, Value: &ast.BoolLit{Position: pass.Position, Value: true}}}
		}
		return ctx.lowerSeqTerms(terms[1:], snapshotName, matchedName, valueTarget)
	}
	var (
		pos        lexer.Pos
		attempt    loweredAttempt
		prefix     []ast.Stmt
		thenBranch []ast.Stmt
	)
	switch n := current.(type) {
	case *ast.GrammarBindTerm:
		pos = n.Term.Pos()
		attempt = ctx.lowerAttempt(n.Term)
		prefix = append([]ast.Stmt{}, attempt.Stmts...)
		thenBranch = append(ctx.markAttemptCommittedStmts(pos, attempt.Committed), &ast.VarDeclStmt{Position: n.Position, Name: n.Name, Value: attempt.Value})
	case *ast.GrammarAssignTerm:
		pos = n.Term.Pos()
		attempt = ctx.lowerAttempt(n.Term)
		prefix = append([]ast.Stmt{}, attempt.Stmts...)
		thenBranch = append(ctx.markAttemptCommittedStmts(pos, attempt.Committed), &ast.AssignStmt{Position: n.Position, Target: &ast.Ident{Position: n.Position, Name: n.Name}, Value: attempt.Value})
		if flagName, ok := ctx.channelSetFlagName(n.Name); ok {
			thenBranch = append(thenBranch, &ast.AssignStmt{Position: n.Position, Target: &ast.Ident{Position: n.Position, Name: flagName}, Value: &ast.BoolLit{Position: n.Position, Value: true}})
		}
	default:
		pos = current.Pos()
		attempt = ctx.lowerAttempt(current)
		prefix = append([]ast.Stmt{}, attempt.Stmts...)
		thenBranch = append(thenBranch, ctx.markAttemptCommittedStmts(pos, attempt.Committed)...)
	}
	if len(terms) == 1 {
		value := attempt.Value
		if assign, ok := current.(*ast.GrammarAssignTerm); ok && ctx.isChannelName(assign.Name) {
			value = zeroedCastExpr(pos, grammarResolvedValueTypeExpr(pos, ctx.production.ReturnType))
		}
		thenBranch = append(thenBranch,
			&ast.AssignStmt{Position: pos, Target: valueTarget, Value: value},
			&ast.AssignStmt{Position: pos, Target: &ast.Ident{Position: pos, Name: matchedName}, Value: &ast.BoolLit{Position: pos, Value: true}},
		)
	} else {
		thenBranch = append(thenBranch, ctx.lowerSeqTerms(terms[1:], snapshotName, matchedName, valueTarget)...)
	}
	elseBranch := []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, pos)}
	prefix = append(prefix, &ast.IfStmt{Position: pos, Cond: attempt.Matched, Then: thenBranch, Else: elseBranch})
	return prefix
}
func (ctx *statefulLowerContext) lowerRequiredAttempt(term *ast.GrammarRequiredTerm) loweredAttempt {
	if term == nil || term.Message == nil || ctx.tokenReceiver == "" {
		if term == nil {
			return loweredAttempt{Matched: &ast.BoolLit{Position: ctx.production.Position, Value: true}, Committed: &ast.BoolLit{Position: ctx.production.Position, Value: false}, Value: &ast.ZeroedLit{Position: ctx.production.Position}}
		}
		return ctx.lowerAttempt(term.Term)
	}
	inner := ctx.lowerAttempt(term.Term)
	value := inner.Value
	if value == nil {
		termType := ctx.termValueTypeOrProduction(term.Position, term.Term)
		value = zeroedCastExpr(term.Position, termType)
	}
	stms := append([]ast.Stmt{}, inner.Stmts...)
	stms = append(stms, &ast.IfStmt{
		Position: term.Position,
		Cond:     &ast.UnaryExpr{Position: term.Position, Op: lexer.TOKEN_NOT, Operand: inner.Matched},
		Then: []ast.Stmt{&ast.ExprStmt{
			Position: term.Position,
			Expr: &ast.CallExpr{
				Position: term.Position,
				Func:     &ast.FieldExpr{Position: term.Position, Object: &ast.Ident{Position: term.Position, Name: ctx.tokenReceiver}, Field: ctx.recordErrorFunc},
				Args:     []ast.Expr{term.Message},
			},
		}},
	})
	return loweredAttempt{Stmts: stms, Matched: &ast.BoolLit{Position: term.Position, Value: true}, Committed: inner.Committed, Value: value}
}
func (ctx *statefulLowerContext) lowerRecoveredAttempt(term *ast.GrammarRecoverTerm) loweredAttempt {
	if term == nil || ctx.tokenReceiver == "" || term.RecoverMsg == nil || len(term.RecoverUntil) == 0 {
		if term == nil {
			return loweredAttempt{Matched: &ast.BoolLit{Position: ctx.production.Position, Value: true}, Committed: &ast.BoolLit{Position: ctx.production.Position, Value: false}, Value: &ast.ZeroedLit{Position: ctx.production.Position}}
		}
		return ctx.lowerAttempt(term.Term)
	}
	inner := ctx.lowerAttempt(term.Term)
	termType := ctx.termValueTypeOrProduction(term.Position, term.Term)
	valueName := ctx.fresh("recover_value")
	valueIdent := &ast.Ident{Position: term.Position, Name: valueName}
	valueInit := zeroedCastExpr(term.Position, termType)
	successValue := inner.Value
	if successValue == nil {
		successValue = zeroedCastExpr(term.Position, termType)
	}
	fallbackValue := term.RecoverValue
	if fallbackValue == nil {
		fallbackValue = zeroedCastExpr(term.Position, termType)
	}
	stmts := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: valueName, Mutable: true, Type: termType, Value: valueInit},
	}
	stmts = append(stmts, inner.Stmts...)
	thenBranch := []ast.Stmt{
		&ast.AssignStmt{Position: term.Position, Target: valueIdent, Value: successValue},
	}
	elseBranch := append([]ast.Stmt{}, ctx.lowerRecoverBody(term.Position, term.RecoverMsg, term.RecoverUntil)...)
	elseBranch = append(elseBranch, &ast.AssignStmt{Position: term.Position, Target: valueIdent, Value: fallbackValue})
	stmts = append(stmts, &ast.IfStmt{Position: term.Position, Cond: inner.Matched, Then: thenBranch, Else: elseBranch})
	return loweredAttempt{Stmts: stmts, Matched: &ast.BoolLit{Position: term.Position, Value: true}, Committed: &ast.BoolLit{Position: term.Position, Value: false}, Value: valueIdent}
}
func (ctx *statefulLowerContext) lowerSuffixAttempt(term *ast.GrammarSuffixTerm) loweredAttempt {
	seedAttempt := ctx.lowerAttempt(term.Seed)
	leftType := ctx.termValueTypeOrProduction(term.Position, term.Seed)
	stopName := ctx.fresh("suffix_stop")
	matchedName := ctx.fresh("suffix_matched")
	committedName := ctx.fresh("suffix_committed")
	snapshotName := ctx.fresh("suffix_cursor")
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
		&ast.VarDeclStmt{Position: term.Position, Name: snapshotName, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
	)
	stmts = append(stmts, ctx.lowerPostfixArms(term.Arms, snapshotName, stopName, matchedName, committedName, leftName)...)
	return loweredAttempt{Stmts: stmts, Matched: &ast.Ident{Position: term.Position, Name: matchedName}, Committed: &ast.Ident{Position: term.Position, Name: committedName}, Value: &ast.Ident{Position: term.Position, Name: leftName}}
}
func (ctx *statefulLowerContext) lowerChoiceAttempt(term *ast.GrammarChoiceTerm) loweredAttempt {
	termType := ctx.termValueTypeOrProduction(term.Position, term)
	matchedName := ctx.fresh("choice_matched")
	committedName := ctx.fresh("choice_committed")
	valueName := ctx.fresh("choice_value")
	snapshotName := ctx.fresh("choice_cursor")
	stmts := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: snapshotName, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
		&ast.VarDeclStmt{Position: term.Position, Name: matchedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.VarDeclStmt{Position: term.Position, Name: committedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.VarDeclStmt{Position: term.Position, Name: valueName, Mutable: true, Type: termType, Value: zeroedCastExpr(term.Position, termType)},
	}
	stmts = append(stmts, ctx.lowerChoiceOptions(term.Options, snapshotName, matchedName, committedName, valueName, termType)...)
	return loweredAttempt{Stmts: stmts, Matched: &ast.Ident{Position: term.Position, Name: matchedName}, Committed: &ast.Ident{Position: term.Position, Name: committedName}, Value: &ast.Ident{Position: term.Position, Name: valueName}}
}
func (ctx *statefulLowerContext) lowerFlatRepeatAttempt(term *ast.GrammarFlatRepeatTerm) loweredAttempt {
	resultType := ctx.termValueTypeOrProduction(term.Position, term)
	resultName := ctx.fresh("flatrepeat_value")
	stopName := ctx.fresh("flatrepeat_stop")
	matchedName := ctx.fresh("flatrepeat_matched")
	committedName := ctx.fresh("flatrepeat_committed")
	itemSnapshot := ctx.fresh("item_cursor")
	itemAttempt := ctx.lowerAttempt(term.Elem)
	loop := &ast.WhileStmt{
		Position: term.Position,
		Cond:     &ast.UnaryExpr{Position: term.Position, Op: lexer.TOKEN_NOT, Operand: &ast.Ident{Position: term.Position, Name: stopName}},
		Body:     ctx.lowerFlatRepeatLoopBody(term, itemSnapshot, resultName, stopName, matchedName, committedName, itemAttempt),
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
