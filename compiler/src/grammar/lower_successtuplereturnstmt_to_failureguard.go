package grammar

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func successTupleReturnStmt(pos lexer.Pos, committed ast.Expr, value ast.Expr) ast.Stmt {
	return &ast.ReturnStmt{
		Position: pos,
		Value: &ast.TupleExpr{
			Position: pos,
			Elems: []ast.Expr{
				&ast.BoolLit{Position: pos, Value: true},
				committed,
				value,
			},
		},
	}
}
func (ctx *statefulLowerContext) lowerChannelPrelude() []ast.Stmt {
	if len(ctx.channels) == 0 {
		return nil
	}
	stmts := make([]ast.Stmt, 0, len(ctx.channels)*2+2)
	if ctx.tokenReceiver != "" {
		startExpr := ctx.currentTokenExpr(ctx.production.Position)
		stmts = append(stmts,
			&ast.VarDeclStmt{Position: ctx.production.Position, Name: "$start", Type: ctx.tokenType, Value: startExpr},
			&ast.VarDeclStmt{Position: ctx.production.Position, Name: "$end", Mutable: true, Type: ctx.tokenType, Value: &ast.Ident{Position: ctx.production.Position, Name: "$start"}},
		)
	}
	for _, channel := range ctx.channels {
		channelType, ok := ctx.channelType(channel)
		if !ok {
			continue
		}
		if flagName, ok := ctx.channelSetFlagName(channel.Name); ok {
			stmts = append(stmts, &ast.VarDeclStmt{Position: channel.Position, Name: flagName, Mutable: true, Type: builtinTypeExpr(channel.Position, "bool"), Value: &ast.BoolLit{Position: channel.Position, Value: false}})
		}
		initValue := zeroedCastExpr(channel.Position, channelType)
		if channel.Default != nil {
			initValue = channel.Default
		}
		stmts = append(stmts, &ast.VarDeclStmt{Position: channel.Position, Name: channel.Name, Mutable: true, Type: channelType, Value: initValue})
	}
	return stmts
}
func (ctx *statefulLowerContext) channelType(channel ast.GrammarChannelDecl) (ast.TypeExpr, bool) {
	if channel.Type != nil {
		return channel.Type, true
	}
	if fieldType, ok := grammarTupleFieldTypeExpr(ctx.production.ReturnType, channel.Name); ok {
		return fieldType, true
	}
	if fieldType, ok := grammarStructFieldTypeExpr(ctx.production.ReturnType, ctx.structScope, channel.Name); ok {
		return fieldType, true
	}
	if _, ok := grammarStructDeclForType(ctx.production.ReturnType, ctx.structScope); ok {
		return nil, false
	}
	if channel.Default == nil && ctx.production.ReturnType != nil {
		return grammarResolvedValueTypeExpr(channel.Position, ctx.production.ReturnType), true
	}
	return nil, false
}
func (ctx *statefulLowerContext) lowerChannelFinalize(pos lexer.Pos) []ast.Stmt {
	if len(ctx.channels) == 0 {
		return nil
	}
	stmts := make([]ast.Stmt, 0, len(ctx.channels)+1)
	if ctx.tokenReceiver != "" {
		stmts = append(stmts, &ast.AssignStmt{Position: pos, Target: &ast.Ident{Position: pos, Name: "$end"}, Value: ctx.currentTokenExpr(pos)})
	}
	for _, channel := range ctx.channels {
		if channel.Default == nil {
			continue
		}
		if _, ok := ctx.channelType(channel); !ok {
			continue
		}
		assign := ast.Stmt(&ast.AssignStmt{Position: pos, Target: &ast.Ident{Position: pos, Name: channel.Name}, Value: channel.Default})
		if flagName, ok := ctx.channelSetFlagName(channel.Name); ok {
			assign = &ast.IfStmt{Position: pos, Cond: &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: &ast.Ident{Position: pos, Name: flagName}}, Then: []ast.Stmt{assign}}
		}
		stmts = append(stmts, assign)
	}
	return stmts
}
func (ctx *statefulLowerContext) channelSetFlagName(name string) (string, bool) {
	for _, channel := range ctx.channels {
		if channel.Name == name && channel.Default != nil {
			return "__grammar_channel_set_" + sanitizeGrammarHelperName(name) + "_" + sanitizeGrammarHelperName(ctx.grammarName) + "_" + sanitizeGrammarHelperName(ctx.production.Name), true
		}
	}
	return "", false
}
func (ctx *statefulLowerContext) isChannelName(name string) bool {
	for _, channel := range ctx.channels {
		if channel.Name == name {
			return true
		}
	}
	return false
}
func (ctx *statefulLowerContext) successTupleReturnStmts(pos lexer.Pos, value ast.Expr) []ast.Stmt {
	stmts := ctx.lowerChannelFinalize(pos)
	return append(stmts, successTupleReturnStmt(pos, ctx.currentCommittedExpr(pos), value))
}
func (ctx *statefulLowerContext) synthesizedChannelReturnExpr(pos lexer.Pos) (ast.Expr, bool) {
	if ctx == nil || len(ctx.channels) == 0 {
		return nil, false
	}
	if fields, ok := grammarTupleLiteralShape(ctx.production.ReturnType); ok {
		channelByName := make(map[string]ast.GrammarChannelDecl, len(ctx.channels))
		for _, channel := range ctx.channels {
			channelByName[channel.Name] = channel
		}
		elems := make([]ast.Expr, 0, len(fields))
		for _, field := range fields {
			channel, ok := channelByName[field.Name]
			if !ok {
				return nil, false
			}
			elems = append(elems, &ast.Ident{Position: pos, Name: channel.Name})
		}
		return &ast.TupleExpr{Position: pos, Elems: elems}, true
	}
	name, typeArgs, ok := grammarStructLiteralShape(ctx.production.ReturnType, ctx.structScope)
	if !ok {
		return nil, false
	}
	decl, ok := grammarStructDeclForType(ctx.production.ReturnType, ctx.structScope)
	if !ok || decl == nil {
		return nil, false
	}
	channelByName := make(map[string]ast.GrammarChannelDecl, len(ctx.channels))
	for _, channel := range ctx.channels {
		channelByName[channel.Name] = channel
	}
	args := make([]ast.Expr, 0, len(decl.Fields))
	argNames := make([]string, 0, len(decl.Fields))
	for _, field := range decl.Fields {
		channel, ok := channelByName[field.Name]
		if !ok {
			return nil, false
		}
		args = append(args, &ast.Ident{Position: pos, Name: channel.Name})
		argNames = append(argNames, channel.Name)
	}
	return &ast.StructLitExpr{Position: pos, Name: name, TypeArgs: typeArgs, Args: args, ArgNames: argNames}, true
}
func failureTupleReturnStmt(pos lexer.Pos, committed ast.Expr, valueType ast.TypeExpr) ast.Stmt {
	return &ast.ReturnStmt{
		Position: pos,
		Value: &ast.TupleExpr{
			Position: pos,
			Elems: []ast.Expr{
				&ast.BoolLit{Position: pos, Value: false},
				committed,
				zeroedCastExpr(pos, grammarResolvedValueTypeExpr(pos, valueType)),
			},
		},
	}
}
func committedAssignTrueStmt(name string, pos lexer.Pos) ast.Stmt {
	return &ast.AssignStmt{Position: pos, Target: &ast.Ident{Position: pos, Name: name}, Value: &ast.BoolLit{Position: pos, Value: true}}
}
func markCommittedStmts(name string, pos lexer.Pos, committed ast.Expr) []ast.Stmt {
	if name == "" || committed == nil {
		return nil
	}
	if lit, ok := committed.(*ast.BoolLit); ok {
		if !lit.Value {
			return nil
		}
		return []ast.Stmt{committedAssignTrueStmt(name, pos)}
	}
	return []ast.Stmt{&ast.IfStmt{Position: pos, Cond: committed, Then: []ast.Stmt{committedAssignTrueStmt(name, pos)}}}
}
func (ctx *statefulLowerContext) currentCommittedExpr(pos lexer.Pos) ast.Expr {
	if ctx.committedName == "" {
		return &ast.BoolLit{Position: pos, Value: false}
	}
	return &ast.Ident{Position: pos, Name: ctx.committedName}
}
func (ctx *statefulLowerContext) markAttemptCommittedStmts(pos lexer.Pos, committed ast.Expr) []ast.Stmt {
	return markCommittedStmts(ctx.committedName, pos, committed)
}
func stateCursorExpr(receiverName string, fieldName string, pos lexer.Pos) ast.Expr {
	return &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: receiverName}, Field: fieldName}
}
func (ctx *statefulLowerContext) currentTokenExpr(pos lexer.Pos) ast.Expr {
	return currentTokenExpr(ctx.tokenReceiver, ctx.currentFunc, pos)
}
func currentTokenExpr(stateName string, currentFunc string, pos lexer.Pos) ast.Expr {
	if currentFunc == "" {
		currentFunc = "current_token"
	}
	return &ast.CallExpr{
		Position: pos,
		Func: &ast.FieldExpr{
			Position: pos,
			Object:   &ast.Ident{Position: pos, Name: stateName},
			Field:    currentFunc,
		},
	}
}
func restoreCursorStmt(receiverName string, fieldName string, snapshotName string, pos lexer.Pos) ast.Stmt {
	return &ast.AssignStmt{
		Position: pos,
		Target:   stateCursorExpr(receiverName, fieldName, pos),
		Value:    &ast.Ident{Position: pos, Name: snapshotName},
	}
}
func stateOwnerExpr(stateName string, pos lexer.Pos) ast.Expr {
	return &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: stateName}, Field: "owner"}
}
func repeatTermAsList(term *ast.GrammarRepeatTerm) *ast.GrammarListTerm {
	if term == nil {
		return nil
	}
	return &ast.GrammarListTerm{Position: term.Position, Elem: term.Elem, Until: append([]ast.GrammarTerm(nil), term.Until...)}
}
func separatedTermAsList(term *ast.GrammarSeparatedTerm) *ast.GrammarListTerm {
	if term == nil {
		return nil
	}
	return &ast.GrammarListTerm{Position: term.Position, Elem: term.Elem, Separator: term.Separator, Until: append([]ast.GrammarTerm(nil), term.Until...)}
}
func listPushStmt(pos lexer.Pos, targetName string, value ast.Expr) ast.Stmt {
	return &ast.ExprStmt{
		Position: pos,
		Expr: &ast.CallExpr{
			Position: pos,
			Func: &ast.FieldExpr{
				Position: pos,
				Object:   &ast.Ident{Position: pos, Name: targetName},
				Field:    "push",
			},
			Args: []ast.Expr{value},
		},
	}
}
func listPushIndexedItemsStmts(ctx *statefulLowerContext, pos lexer.Pos, targetName string, sourceName string) []ast.Stmt {
	indexName := ctx.fresh("flatrepeat_index")
	indexIdent := &ast.Ident{Position: pos, Name: indexName}
	sourceIdent := &ast.Ident{Position: pos, Name: sourceName}
	return []ast.Stmt{
		&ast.VarDeclStmt{Position: pos, Name: indexName, Mutable: true, Type: builtinTypeExpr(pos, "usize"), Value: &ast.IntLit{Position: pos, Value: "0"}},
		&ast.WhileStmt{
			Position: pos,
			Cond:     &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_LT, Left: indexIdent, Right: &ast.FieldExpr{Position: pos, Object: sourceIdent, Field: "count"}},
			Body: []ast.Stmt{
				listPushStmt(pos, targetName, &ast.IndexExpr{Position: pos, Object: sourceIdent, Index: indexIdent}),
				&ast.AugAssignStmt{Position: pos, Op: lexer.TOKEN_PLUSEQ, Target: indexIdent, Value: &ast.IntLit{Position: pos, Value: "1"}},
			},
		},
	}
}
func listPushIndexedItemsExprStmts(ctx lowerContext, pos lexer.Pos, targetName string, sourceName string) []ast.Stmt {
	indexName := ctx.fresh("flatten_group_index")
	indexIdent := &ast.Ident{Position: pos, Name: indexName}
	sourceIdent := &ast.Ident{Position: pos, Name: sourceName}
	return []ast.Stmt{
		&ast.VarDeclStmt{Position: pos, Name: indexName, Mutable: true, Type: builtinTypeExpr(pos, "usize"), Value: &ast.IntLit{Position: pos, Value: "0"}},
		&ast.WhileStmt{
			Position: pos,
			Cond:     &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_LT, Left: indexIdent, Right: &ast.FieldExpr{Position: pos, Object: sourceIdent, Field: "count"}},
			Body: []ast.Stmt{
				listPushStmt(pos, targetName, &ast.IndexExpr{Position: pos, Object: sourceIdent, Index: indexIdent}),
				&ast.AugAssignStmt{Position: pos, Op: lexer.TOKEN_PLUSEQ, Target: indexIdent, Value: &ast.IntLit{Position: pos, Value: "1"}},
			},
		},
	}
}
func grammarTokenMatchExpr(pos lexer.Pos, tokenExpr ast.Expr, tokenKindField string, tokenLookupFunc string, value string) ast.Expr {
	if tokenLookupFunc == "" {
		tokenLookupFunc = "token_kind_for_text"
	}
	return grammarTokenKindMatchExpr(pos, tokenExpr, tokenKindField, &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: tokenLookupFunc},
		Args:     []ast.Expr{&ast.StringLit{Position: pos, Value: value}},
	})
}
func grammarTokenKindMatchExpr(pos lexer.Pos, tokenExpr ast.Expr, tokenKindField string, kindExpr ast.Expr) ast.Expr {
	return &ast.BinaryExpr{
		Position: pos,
		Op:       lexer.TOKEN_EQEQ,
		Left:     tokenKindFieldExpr(pos, tokenExpr, tokenKindField),
		Right:    kindExpr,
	}
}
func tokenKindFieldExpr(pos lexer.Pos, tokenExpr ast.Expr, tokenKindField string) ast.Expr {
	if tokenKindField == "" {
		tokenKindField = "kind"
	}
	return &ast.FieldExpr{Position: pos, Object: tokenExpr, Field: tokenKindField}
}
func grammarTokenKindMatcher(term *ast.GrammarCallTerm) (ast.Expr, bool) {
	if term == nil || term.Name != "token" || !term.Explicit || len(term.Args) != 1 {
		return nil, false
	}
	return term.Args[0], true
}
func grammarTokenKindExpr(pos lexer.Pos, tokenKindType ast.TypeExpr, kind string) ast.Expr {
	return &ast.FieldExpr{Position: pos, Object: grammarTokenKindTypeExpr(pos, tokenKindType), Field: kind}
}
func grammarTokenKindTypeExpr(pos lexer.Pos, tokenKindType ast.TypeExpr) ast.Expr {
	switch n := tokenKindType.(type) {
	case *ast.NamedType:
		return &ast.Ident{Position: pos, Name: n.Name}
	default:
		return &ast.Ident{Position: pos, Name: "TokenKind"}
	}
}
func grammarExpectFuncExpr(pos lexer.Pos, tokenReceiver string, name string) ast.Expr {
	funcExpr := ast.Expr(&ast.Ident{Position: pos, Name: name})
	if tokenReceiver != "" {
		funcExpr = &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: tokenReceiver}, Field: name}
	}
	return funcExpr
}
func (ctx *statefulLowerContext) fresh(prefix string) string {
	ctx.tempCounter++
	return "__grammar_" + prefix + "_" + ctx.production.Name + "_" + sanitizeGrammarHelperName(ctx.grammarName) + "_" + strings.TrimPrefix(strings.TrimSpace(strings.Trim(prefix, "_")), "") + "_" + itoa(ctx.tempCounter)
}
func (ctx *statefulLowerContext) lowerExprContext() lowerContext {
	return lowerContext{
		tokenReceiver:  ctx.tokenReceiver,
		tokenKindType:  ctx.tokenKindType,
		tokenKindField: ctx.tokenKindField,
		expectFunc:     ctx.expectFunc,
		expectKindFunc: ctx.expectKindFunc,
		eofExpr:        ctx.eofExpr,
		allocExpr:      ctx.allocExpr,
		returnType:     ctx.production.ReturnType,
		tempCounter:    &ctx.tempCounter,
	}
}
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buf := [32]byte{}
	index := len(buf)
	for value > 0 {
		index--
		buf[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		buf[index] = '-'
	}
	return string(buf[index:])
}

type loweredAttempt struct {
	Stmts     []ast.Stmt
	Matched   ast.Expr
	Committed ast.Expr
	Value     ast.Expr
}

func (ctx *statefulLowerContext) lowerSequentialTerm(term ast.GrammarTerm, snapshotName string) []ast.Stmt {
	switch n := term.(type) {
	case *ast.GrammarPassTerm:
		return nil
	case *ast.GrammarReturnTerm:
		return ctx.lowerExplicitReturnStmts(n, snapshotName)
	case *ast.GrammarBindTerm:
		facts := ctx.termFacts(n.Term)
		attempt := ctx.lowerAttempt(n.Term)
		result := append([]ast.Stmt{}, attempt.Stmts...)
		result = append(result, ctx.markAttemptCommittedStmts(n.Term.Pos(), attempt.Committed)...)
		if facts.CanFail {
			result = append(result, ctx.failureGuard(n.Term.Pos(), snapshotName, attempt.Matched)...)
		}
		result = append(result, &ast.VarDeclStmt{Position: n.Position, Name: n.Name, Value: attempt.Value})
		return result
	case *ast.GrammarAssignTerm:
		facts := ctx.termFacts(n.Term)
		attempt := ctx.lowerAttempt(n.Term)
		result := append([]ast.Stmt{}, attempt.Stmts...)
		result = append(result, ctx.markAttemptCommittedStmts(n.Term.Pos(), attempt.Committed)...)
		if facts.CanFail {
			result = append(result, ctx.failureGuard(n.Term.Pos(), snapshotName, attempt.Matched)...)
		}
		result = append(result, &ast.AssignStmt{Position: n.Position, Target: &ast.Ident{Position: n.Position, Name: n.Name}, Value: attempt.Value})
		if flagName, ok := ctx.channelSetFlagName(n.Name); ok {
			result = append(result, &ast.AssignStmt{Position: n.Position, Target: &ast.Ident{Position: n.Position, Name: flagName}, Value: &ast.BoolLit{Position: n.Position, Value: true}})
		}
		return result
	default:
		facts := ctx.termFacts(term)
		attempt := ctx.lowerAttempt(term)
		result := append([]ast.Stmt{}, attempt.Stmts...)
		result = append(result, ctx.markAttemptCommittedStmts(term.Pos(), attempt.Committed)...)
		if facts.CanFail {
			result = append(result, ctx.failureGuard(term.Pos(), snapshotName, attempt.Matched)...)
		}
		if call, ok := term.(*ast.GrammarCallTerm); ok && !facts.CanFail && attempt.Value != nil && ctx.callTermReturnsValue(call) {
			result = append(result, &ast.ExprStmt{Position: term.Pos(), Expr: attempt.Value})
		}
		return result
	}
}
func (ctx *statefulLowerContext) lowerExplicitReturnStmts(term *ast.GrammarReturnTerm, snapshotName string) []ast.Stmt {
	if term == nil {
		return nil
	}
	if term.Term == nil {
		return ctx.successTupleReturnStmts(term.Position, nil)
	}
	if expr, ok := term.Term.(*ast.GrammarExprTerm); ok && expr != nil {
		return ctx.successTupleReturnStmts(term.Position, expr.Expr)
	}
	facts := ctx.termFacts(term.Term)
	attempt := ctx.lowerAttempt(term.Term)
	result := append([]ast.Stmt{}, attempt.Stmts...)
	result = append(result, ctx.markAttemptCommittedStmts(term.Term.Pos(), attempt.Committed)...)
	if facts.CanFail {
		result = append(result, ctx.failureGuard(term.Term.Pos(), snapshotName, attempt.Matched)...)
	}
	result = append(result, ctx.successTupleReturnStmts(term.Position, attempt.Value)...)
	return result
}
func (ctx *statefulLowerContext) callTermReturnsValue(term *ast.GrammarCallTerm) bool {
	if term == nil {
		return true
	}
	if _, ok := grammarTokenKindMatcher(term); ok {
		return true
	}
	_, production, ok := ctx.resolveGrammarProductionInfo(term)
	if !ok {
		return true
	}
	return production.ReturnType != nil
}
func (ctx *statefulLowerContext) failureGuard(pos lexer.Pos, snapshotName string, matched ast.Expr) []ast.Stmt {
	return []ast.Stmt{
		&ast.IfStmt{
			Position: pos,
			Cond:     &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: matched},
			Then: []ast.Stmt{
				restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, pos),
				failureTupleReturnStmt(pos, ctx.currentCommittedExpr(pos), ctx.production.ReturnType),
			},
		},
	}
}
