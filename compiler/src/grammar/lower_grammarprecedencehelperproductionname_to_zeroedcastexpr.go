package grammar

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func grammarPrecedenceHelperProductionName(grammarName string, productionName string, blockIndex int, levelName string) string {
	return "__grammar_precedence_" + sanitizeGrammarHelperName(grammarName) + "_" + sanitizeGrammarHelperName(productionName) + "_" + itoa(blockIndex) + "_" + sanitizeGrammarHelperName(levelName)
}
func grammarProductionParamArgs(params []ast.ParamDecl) []ast.Expr {
	args := make([]ast.Expr, 0, len(params))
	for _, param := range params {
		args = append(args, &ast.Ident{Position: param.Position, Name: param.Name})
	}
	return args
}
func grammarNamedPrecedenceResultName(helperName string) string {
	return "__grammar_precedence_result_" + sanitizeGrammarHelperName(helperName)
}
func buildNamedPrecedenceHelperProduction(production ast.GrammarProductionDecl, helperName string, level ast.GrammarPrecedenceLevel, seed ast.GrammarTerm, arms []ast.GrammarPrecedenceArm) ast.GrammarProductionDecl {
	resultName := grammarNamedPrecedenceResultName(helperName)
	term := seed
	if level.LeftName != "" {
		term = &ast.GrammarPrecedenceTerm{Position: level.Position, Assoc: level.Assoc, LeftName: level.LeftName, Seed: seed, Arms: arms}
	}
	return ast.GrammarProductionDecl{
		Position:     level.Position,
		Public:       production.Public,
		Name:         helperName,
		HasParamList: true,
		Params:       append([]ast.ParamDecl(nil), production.Params...),
		ReturnType:   production.ReturnType,
		Terms: []ast.GrammarTerm{
			&ast.GrammarBindTerm{Position: level.Position, Name: resultName, Term: term},
			&ast.GrammarReturnTerm{Position: level.Position, Term: &ast.GrammarExprTerm{Position: level.Position, Expr: &ast.Ident{Position: level.Position, Name: resultName}}},
		},
	}
}
func applyNamedPrecedenceAssociativity(level ast.GrammarPrecedenceLevel, helperName string, paramArgs []ast.Expr, seed ast.GrammarTerm, arms []ast.GrammarPrecedenceArm) []ast.GrammarPrecedenceArm {
	if level.Assoc == "" || level.LeftName == "" || len(arms) == 0 {
		return arms
	}
	updated := make([]ast.GrammarPrecedenceArm, 0, len(arms))
	for _, arm := range arms {
		if grammarPrecedenceArmHasBinding(arm, "right") {
			updated = append(updated, arm)
			continue
		}
		rightTerm := seed
		if level.Assoc == ast.GrammarAssociativityRight {
			rightTerm = &ast.GrammarCallTerm{Position: arm.Position, Name: helperName, Explicit: true, Args: append([]ast.Expr(nil), paramArgs...)}
		}
		bindings := append([]*ast.GrammarBindTerm(nil), arm.Bindings...)
		bindings = append(bindings, &ast.GrammarBindTerm{Position: arm.Position, Name: "right", Term: rightTerm})
		arm.Bindings = bindings
		updated = append(updated, arm)
	}
	return updated
}
func grammarPrecedenceArmHasBinding(arm ast.GrammarPrecedenceArm, name string) bool {
	for _, binding := range arm.Bindings {
		if binding != nil && binding.Name == name {
			return true
		}
	}
	return false
}
func rewriteNamedPrecedenceArmCalls(arm ast.GrammarPrecedenceArm, helperNames map[string]string, paramArgs []ast.Expr) ast.GrammarPrecedenceArm {
	op := rewriteNamedPrecedenceHelperCalls(arm.Op, helperNames, paramArgs)
	bindings := make([]*ast.GrammarBindTerm, 0, len(arm.Bindings))
	for _, binding := range arm.Bindings {
		bindings = append(bindings, &ast.GrammarBindTerm{Position: binding.Position, Name: binding.Name, Term: rewriteNamedPrecedenceHelperCalls(binding.Term, helperNames, paramArgs)})
	}
	return ast.GrammarPrecedenceArm{Position: arm.Position, OpName: arm.OpName, Op: op, Block: arm.Block, Bindings: bindings, Value: arm.Value}
}
func rewriteNamedPrecedenceHelperCalls(term ast.GrammarTerm, helperNames map[string]string, paramArgs []ast.Expr) ast.GrammarTerm {
	switch n := term.(type) {
	case *ast.GrammarCallTerm:
		if n.Explicit && len(n.Args) == 0 {
			if helperName, ok := helperNames[n.Name]; ok {
				return &ast.GrammarCallTerm{Position: n.Position, Name: helperName, Explicit: true, Args: append([]ast.Expr(nil), paramArgs...)}
			}
		}
		return n
	case *ast.GrammarBindTerm:
		return &ast.GrammarBindTerm{Position: n.Position, Name: n.Name, Term: rewriteNamedPrecedenceHelperCalls(n.Term, helperNames, paramArgs)}
	case *ast.GrammarAssignTerm:
		return &ast.GrammarAssignTerm{Position: n.Position, Name: n.Name, Term: rewriteNamedPrecedenceHelperCalls(n.Term, helperNames, paramArgs)}
	case *ast.GrammarReturnTerm:
		return &ast.GrammarReturnTerm{Position: n.Position, Term: rewriteNamedPrecedenceHelperCalls(n.Term, helperNames, paramArgs)}
	case *ast.GrammarChoiceTerm:
		options := make([]ast.GrammarTerm, 0, len(n.Options))
		for _, option := range n.Options {
			options = append(options, rewriteNamedPrecedenceHelperCalls(option, helperNames, paramArgs))
		}
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: options}
	case *ast.GrammarOptionalTerm:
		return &ast.GrammarOptionalTerm{Position: n.Position, Term: rewriteNamedPrecedenceHelperCalls(n.Term, helperNames, paramArgs)}
	case *ast.GrammarRequiredTerm:
		return &ast.GrammarRequiredTerm{Position: n.Position, Term: rewriteNamedPrecedenceHelperCalls(n.Term, helperNames, paramArgs), Message: n.Message}
	case *ast.GrammarDelimitedTerm:
		return &ast.GrammarDelimitedTerm{Position: n.Position, Open: rewriteNamedPrecedenceHelperCalls(n.Open, helperNames, paramArgs), Body: rewriteNamedPrecedenceHelperCalls(n.Body, helperNames, paramArgs), Close: rewriteNamedPrecedenceHelperCalls(n.Close, helperNames, paramArgs), Message: n.Message}
	case *ast.GrammarSeqTerm:
		terms := make([]ast.GrammarTerm, 0, len(n.Terms))
		for _, term := range n.Terms {
			terms = append(terms, rewriteNamedPrecedenceHelperCalls(term, helperNames, paramArgs))
		}
		return &ast.GrammarSeqTerm{Position: n.Position, Terms: terms}
	case *ast.GrammarLookaheadTerm:
		return &ast.GrammarLookaheadTerm{Position: n.Position, Term: rewriteNamedPrecedenceHelperCalls(n.Term, helperNames, paramArgs)}
	case *ast.GrammarSingletonTerm:
		return &ast.GrammarSingletonTerm{Position: n.Position, Type: n.Type, Value: n.Value}
	case *ast.GrammarEmptyTerm:
		return &ast.GrammarEmptyTerm{Position: n.Position, Type: n.Type}
	case *ast.GrammarConcatTerm:
		terms := make([]ast.GrammarTerm, 0, len(n.Terms))
		for _, term := range n.Terms {
			terms = append(terms, rewriteNamedPrecedenceHelperCalls(term, helperNames, paramArgs))
		}
		return &ast.GrammarConcatTerm{Position: n.Position, Terms: terms}
	case *ast.GrammarListTerm:
		var separator ast.GrammarTerm
		if n.Separator != nil {
			separator = rewriteNamedPrecedenceHelperCalls(n.Separator, helperNames, paramArgs)
		}
		until := make([]ast.GrammarTerm, 0, len(n.Until))
		for _, stop := range n.Until {
			until = append(until, rewriteNamedPrecedenceHelperCalls(stop, helperNames, paramArgs))
		}
		return &ast.GrammarListTerm{Position: n.Position, Elem: rewriteNamedPrecedenceHelperCalls(n.Elem, helperNames, paramArgs), Separator: separator, Until: until}
	case *ast.GrammarRepeatTerm:
		until := make([]ast.GrammarTerm, 0, len(n.Until))
		for _, stop := range n.Until {
			until = append(until, rewriteNamedPrecedenceHelperCalls(stop, helperNames, paramArgs))
		}
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: rewriteNamedPrecedenceHelperCalls(n.Elem, helperNames, paramArgs), Until: until, MinOne: n.MinOne}
	case *ast.GrammarFlatRepeatTerm:
		until := make([]ast.GrammarTerm, 0, len(n.Until))
		for _, stop := range n.Until {
			until = append(until, rewriteNamedPrecedenceHelperCalls(stop, helperNames, paramArgs))
		}
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: rewriteNamedPrecedenceHelperCalls(n.Elem, helperNames, paramArgs), Until: until}
	case *ast.GrammarWhileTerm:
		until := make([]ast.GrammarTerm, 0, len(n.Until))
		for _, stop := range n.Until {
			until = append(until, rewriteNamedPrecedenceHelperCalls(stop, helperNames, paramArgs))
		}
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: rewriteNamedPrecedenceHelperCalls(n.Elem, helperNames, paramArgs), Until: until}
	case *ast.GrammarSeparatedTerm:
		until := make([]ast.GrammarTerm, 0, len(n.Until))
		for _, stop := range n.Until {
			until = append(until, rewriteNamedPrecedenceHelperCalls(stop, helperNames, paramArgs))
		}
		return &ast.GrammarSeparatedTerm{Position: n.Position, Elem: rewriteNamedPrecedenceHelperCalls(n.Elem, helperNames, paramArgs), Separator: rewriteNamedPrecedenceHelperCalls(n.Separator, helperNames, paramArgs), Until: until}
	case *ast.GrammarPrecedenceTerm:
		if len(n.Levels) != 0 {
			return n
		}
		arms := make([]ast.GrammarPrecedenceArm, 0, len(n.Arms))
		for _, arm := range n.Arms {
			arms = append(arms, rewriteNamedPrecedenceArmCalls(arm, helperNames, paramArgs))
		}
		return &ast.GrammarPrecedenceTerm{Position: n.Position, Assoc: n.Assoc, LeftName: n.LeftName, Seed: rewriteNamedPrecedenceHelperCalls(n.Seed, helperNames, paramArgs), Arms: arms}
	default:
		return term
	}
}
func grammarDeclIsStateful(decl *ast.GrammarDecl) bool {
	if decl == nil {
		return false
	}
	for _, production := range decl.Productions {
		if grammarReceiverInfoForProduction(decl, production).cursorReceiver != "" {
			return true
		}
	}
	return false
}
func grammarHeaderStateParam(grammarDecl *ast.GrammarDecl, pos lexer.Pos) (ast.ParamDecl, grammarStateReceiverInfo, bool) {
	if grammarDecl == nil || grammarDecl.UsingType == nil || grammarDecl.CursorExpr == nil {
		return ast.ParamDecl{}, grammarStateReceiverInfo{}, false
	}
	receiverName, cursorField, ok := grammarHeaderCursorBinding(grammarDecl)
	if !ok || receiverName == "" || cursorField == "" {
		return ast.ParamDecl{}, grammarStateReceiverInfo{}, false
	}
	info := grammarStateReceiverInfo{cursorReceiver: receiverName, cursorField: cursorField}
	if grammarDecl.OverType != nil {
		info.tokenReceiver = receiverName
	}
	paramType := ast.TypeExpr(&ast.MutableType{
		Position: pos,
		Elem: &ast.RefType{
			Position: pos,
			Elem:     grammarDecl.UsingType,
			State:    ast.RefStateNonNull,
			Storage:  ast.RefStorageAny,
		},
	})
	return ast.ParamDecl{Position: pos, Name: receiverName, Type: paramType}, info, true
}
func grammarHeaderCursorBinding(grammarDecl *ast.GrammarDecl) (string, string, bool) {
	if grammarDecl == nil || grammarDecl.CursorExpr == nil {
		return "", "", false
	}
	switch n := grammarDecl.CursorExpr.(type) {
	case *ast.Ident:
		fieldName := "pos"
		if grammarDecl.OverType != nil {
			fieldName = "cursor"
		}
		return n.Name, fieldName, true
	case *ast.FieldExpr:
		receiver, ok := n.Object.(*ast.Ident)
		if !ok || receiver.Name == "" || n.Field == "" {
			return "", "", false
		}
		return receiver.Name, n.Field, true
	default:
		return "", "", false
	}
}
func grammarReceiverInfoForProduction(grammarDecl *ast.GrammarDecl, production ast.GrammarProductionDecl) grammarStateReceiverInfo {
	if headerParam, headerInfo, ok := grammarHeaderStateParam(grammarDecl, production.Position); ok {
		if len(production.Params) != 0 && production.Params[0].Name == headerParam.Name {
			return headerInfo
		}
	}
	if explicit := grammarStateReceiver(production.Params); explicit.cursorReceiver != "" {
		return explicit
	}
	if _, headerInfo, ok := grammarHeaderStateParam(grammarDecl, production.Position); ok {
		return headerInfo
	}
	return grammarStateReceiverInfo{}
}
func grammarTryFuncName(grammarName string, productionName string) string {
	baseGrammar := sanitizeGrammarHelperName(grammarName)
	baseProduction := sanitizeGrammarHelperName(productionName)
	return "__grammar_try__" + baseGrammar + "__" + baseProduction
}
func grammarPublicTryFuncName(grammarName string, productionName string) string {
	baseGrammar := sanitizeGrammarHelperName(grammarName)
	baseProduction := sanitizeGrammarHelperName(productionName)
	return "grammar_try_" + baseGrammar + "_" + baseProduction
}
func sanitizeGrammarHelperName(value string) string {
	if value == "" {
		return "anon"
	}
	value = strings.ReplaceAll(value, ".", "_")
	value = strings.ReplaceAll(value, "-", "_")
	return value
}
func lowerStatefulPublicProduction(grammarDecl *ast.GrammarDecl, ctx *statefulLowerContext) *ast.FuncDecl {
	callArgs := make([]ast.Expr, 0, len(ctx.production.Params))
	for _, param := range ctx.production.Params {
		callArgs = append(callArgs, &ast.Ident{Position: param.Position, Name: param.Name})
	}
	matchedName := ctx.fresh("matched")
	committedName := ctx.fresh("committed")
	valueName := ctx.fresh("value")
	tryCall := &ast.CallExpr{
		Position: ctx.production.Position,
		Func:     &ast.Ident{Position: ctx.production.Position, Name: grammarTryFuncName(ctx.grammarName, ctx.production.Name)},
		Args:     callArgs,
	}
	body := []ast.Stmt{
		&ast.TupleBindStmt{
			Position: ctx.production.Position,
			Names: []ast.TupleBindName{
				{Position: ctx.production.Position, Name: matchedName},
				{Position: ctx.production.Position, Name: committedName},
				{Position: ctx.production.Position, Name: valueName},
			},
			Declare: true,
			Value:   grammarMaybeTryExpr(tryCall, ctx.production.ReturnType),
		},
	}
	if ctx.production.RecoverMsg != nil && len(ctx.production.RecoverUntil) != 0 {
		body = append(body, ctx.lowerRecoveryClause(matchedName)...)
	}
	if ctx.production.ReturnType != nil {
		body = append(body, &ast.ReturnStmt{Position: ctx.production.Position, Value: &ast.Ident{Position: ctx.production.Position, Name: valueName}})
	}
	return &ast.FuncDecl{
		Position:         ctx.production.Position,
		Name:             ctx.production.Name,
		TypeParams:       append([]string(nil), grammarDecl.TypeParams...),
		RegionParams:     append([]string(nil), grammarDecl.RegionParams...),
		PermissionParams: append([]string(nil), grammarDecl.PermissionParams...),
		GenericParams:    append([]ast.GenericParam(nil), grammarDecl.GenericParams...),
		Params:           append([]ast.ParamDecl(nil), ctx.production.Params...),
		ReturnType:       ctx.production.ReturnType,
		Body:             grammarAllocScopedCanBlock(ctx.production.Position, grammarDecl, ctx.allocExpr, body),
	}
}
func lowerStatefulPublicTryProduction(grammarDecl *ast.GrammarDecl, ctx *statefulLowerContext) *ast.FuncDecl {
	callArgs := make([]ast.Expr, 0, len(ctx.production.Params))
	for _, param := range ctx.production.Params {
		callArgs = append(callArgs, &ast.Ident{Position: param.Position, Name: param.Name})
	}
	matchedName := ctx.fresh("matched")
	committedName := ctx.fresh("committed")
	valueName := ctx.fresh("value")
	tryCall := ast.Expr(&ast.CallExpr{
		Position: ctx.production.Position,
		Func:     &ast.Ident{Position: ctx.production.Position, Name: grammarTryFuncName(ctx.grammarName, ctx.production.Name)},
		Args:     callArgs,
	})
	if grammarErrorTypeExpr(ctx.production.ReturnType) != nil {
		tryCall = grammarMaybeTryExpr(tryCall, ctx.production.ReturnType)
	}
	return &ast.FuncDecl{
		Position:         ctx.production.Position,
		Name:             grammarPublicTryFuncName(ctx.grammarName, ctx.production.Name),
		TypeParams:       append([]string(nil), grammarDecl.TypeParams...),
		RegionParams:     append([]string(nil), grammarDecl.RegionParams...),
		PermissionParams: append([]string(nil), grammarDecl.PermissionParams...),
		GenericParams:    append([]ast.GenericParam(nil), grammarDecl.GenericParams...),
		Params:           append([]ast.ParamDecl(nil), ctx.production.Params...),
		ReturnType:       grammarTryReturnTypeExpr(ctx.production.Position, ctx.production.ReturnType),
		Body: grammarCanBlock(ctx.production.Position, []ast.Stmt{
			&ast.TupleBindStmt{
				Position: ctx.production.Position,
				Names: []ast.TupleBindName{
					{Position: ctx.production.Position, Name: matchedName},
					{Position: ctx.production.Position, Name: committedName},
					{Position: ctx.production.Position, Name: valueName},
				},
				Declare: true,
				Value:   tryCall,
			},
			&ast.ReturnStmt{Position: ctx.production.Position, Value: &ast.TupleExpr{Position: ctx.production.Position, Elems: []ast.Expr{&ast.Ident{Position: ctx.production.Position, Name: matchedName}, &ast.Ident{Position: ctx.production.Position, Name: valueName}}}},
		}),
	}
}
func (ctx *statefulLowerContext) lowerRecoveryClause(matchedName string) []ast.Stmt {
	recoverBody := ctx.lowerRecoverBody(ctx.production.Position, ctx.production.RecoverMsg, ctx.production.RecoverUntil)
	if len(recoverBody) == 0 {
		return nil
	}
	thenBody := append([]ast.Stmt{}, recoverBody...)
	if ctx.production.RecoverValue != nil && ctx.production.ReturnType != nil {
		thenBody = append(thenBody, &ast.ReturnStmt{Position: ctx.production.Position, Value: ctx.production.RecoverValue})
	}
	return []ast.Stmt{
		&ast.IfStmt{
			Position: ctx.production.Position,
			Cond:     &ast.UnaryExpr{Position: ctx.production.Position, Op: lexer.TOKEN_NOT, Operand: &ast.Ident{Position: ctx.production.Position, Name: matchedName}},
			Then:     thenBody,
		},
	}
}
func (ctx *statefulLowerContext) lowerRecoverBody(pos lexer.Pos, message ast.Expr, until []ast.GrammarTerm) []ast.Stmt {
	if ctx.tokenReceiver == "" || message == nil || len(until) == 0 {
		return nil
	}
	stopCond := ctx.lowerListUntilMatchExpr(pos, until)
	if stopCond == nil {
		return nil
	}
	return []ast.Stmt{
		&ast.ExprStmt{
			Position: pos,
			Expr: &ast.CallExpr{
				Position: pos,
				Func:     &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: ctx.tokenReceiver}, Field: ctx.recordErrorFunc},
				Args:     []ast.Expr{message},
			},
		},
		&ast.WhileStmt{
			Position: pos,
			Cond: &ast.BinaryExpr{
				Position: pos,
				Op:       lexer.TOKEN_AND,
				Left:     &ast.UnaryExpr{Position: pos, Op: lexer.TOKEN_NOT, Operand: stopCond},
				Right: &ast.BinaryExpr{
					Position: pos,
					Op:       lexer.TOKEN_BANGEQ,
					Left:     tokenKindFieldExpr(pos, ctx.currentTokenExpr(pos), ctx.tokenKindField),
					Right:    cloneHeaderExprAtPos(ctx.eofExpr, pos),
				},
			},
			Body: []ast.Stmt{
				&ast.ExprStmt{Position: pos, Expr: &ast.CallExpr{Position: pos, Func: &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: ctx.tokenReceiver}, Field: ctx.advanceFunc}}},
			},
		},
	}
}
func lowerStatefulTryProduction(grammarDecl *ast.GrammarDecl, ctx *statefulLowerContext) *ast.FuncDecl {
	snapshotName := ctx.fresh("cursor_start")
	committedName := ctx.fresh("committed")
	ctx.committedName = committedName
	body := []ast.Stmt{
		&ast.VarDeclStmt{
			Position: ctx.production.Position,
			Name:     snapshotName,
			Value:    stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, ctx.production.Position),
		},
		&ast.VarDeclStmt{Position: ctx.production.Position, Name: committedName, Mutable: true, Type: builtinTypeExpr(ctx.production.Position, "bool"), Value: &ast.BoolLit{Position: ctx.production.Position, Value: false}},
	}
	body = append(body, ctx.lowerChannelPrelude()...)
	for _, term := range ctx.production.Terms {
		body = append(body, ctx.lowerSequentialTerm(term, snapshotName)...)
	}
	successValue := zeroedCastExpr(ctx.production.Position, grammarResolvedValueTypeExpr(ctx.production.Position, ctx.production.ReturnType))
	if synthesized, ok := ctx.synthesizedChannelReturnExpr(ctx.production.Position); ok {
		successValue = synthesized
	}
	body = append(body, ctx.successTupleReturnStmts(ctx.production.Position, successValue)...)
	return &ast.FuncDecl{
		Position:         ctx.production.Position,
		Name:             grammarTryFuncName(ctx.grammarName, ctx.production.Name),
		TypeParams:       append([]string(nil), grammarDecl.TypeParams...),
		RegionParams:     append([]string(nil), grammarDecl.RegionParams...),
		PermissionParams: append([]string(nil), grammarDecl.PermissionParams...),
		GenericParams:    append([]ast.GenericParam(nil), grammarDecl.GenericParams...),
		Params:           append([]ast.ParamDecl(nil), ctx.production.Params...),
		ReturnType:       grammarInternalTryReturnTypeExpr(ctx.production.Position, ctx.production.ReturnType),
		Body:             grammarAllocScopedCanBlock(ctx.production.Position, grammarDecl, ctx.allocExpr, body),
	}
}
func grammarInternalTryReturnTypeExpr(pos lexer.Pos, valueType ast.TypeExpr) ast.TypeExpr {
	tupleType := &ast.TupleTypeExpr{
		Position: pos,
		Fields: []ast.TupleTypeField{
			{Position: pos, Name: "matched", Type: builtinTypeExpr(pos, "bool")},
			{Position: pos, Name: "committed", Type: builtinTypeExpr(pos, "bool")},
			{Position: pos, Name: "value", Type: grammarResolvedValueTypeExpr(pos, valueType)},
		},
	}
	if errType := grammarErrorTypeExpr(valueType); errType != nil {
		return &ast.ErrorUnionTypeExpr{Position: pos, Value: tupleType, Errors: errType}
	}
	return tupleType
}
func grammarTryReturnTypeExpr(pos lexer.Pos, valueType ast.TypeExpr) ast.TypeExpr {
	tupleType := &ast.TupleTypeExpr{
		Position: pos,
		Fields: []ast.TupleTypeField{
			{Position: pos, Name: "matched", Type: builtinTypeExpr(pos, "bool")},
			{Position: pos, Name: "value", Type: grammarResolvedValueTypeExpr(pos, valueType)},
		},
	}
	if errType := grammarErrorTypeExpr(valueType); errType != nil {
		return &ast.ErrorUnionTypeExpr{Position: pos, Value: tupleType, Errors: errType}
	}
	return tupleType
}
func grammarValueTypeExpr(valueType ast.TypeExpr) ast.TypeExpr {
	if errType, ok := valueType.(*ast.ErrorUnionTypeExpr); ok && errType != nil {
		return errType.Value
	}
	return valueType
}
func grammarResolvedValueTypeExpr(pos lexer.Pos, valueType ast.TypeExpr) ast.TypeExpr {
	resolved := grammarValueTypeExpr(valueType)
	if resolved != nil {
		return resolved
	}
	return builtinTypeExpr(pos, "bool")
}
func grammarStructLiteralShape(valueType ast.TypeExpr, structScope map[string]*ast.StructDecl) (string, []ast.TypeExpr, bool) {
	switch n := grammarValueTypeExpr(valueType).(type) {
	case *ast.NamedType:
		if _, ok := grammarStructDeclForTypeName(n.Name, structScope); ok {
			return n.Name, nil, true
		}
	case *ast.GenericType:
		if _, ok := grammarStructDeclForTypeName(n.Name, structScope); ok {
			return n.Name, append([]ast.TypeExpr(nil), n.Args...), true
		}
	}
	return "", nil, false
}
func grammarStructDeclForTypeName(name string, structScope map[string]*ast.StructDecl) (*ast.StructDecl, bool) {
	if name == "" || len(structScope) == 0 {
		return nil, false
	}
	if decl, ok := structScope[name]; ok {
		return decl, decl != nil
	}
	return nil, false
}
func grammarStructDeclForType(valueType ast.TypeExpr, structScope map[string]*ast.StructDecl) (*ast.StructDecl, bool) {
	switch n := grammarValueTypeExpr(valueType).(type) {
	case *ast.NamedType:
		return grammarStructDeclForTypeName(n.Name, structScope)
	case *ast.GenericType:
		return grammarStructDeclForTypeName(n.Name, structScope)
	default:
		return nil, false
	}
}
func grammarStructFieldTypeExpr(valueType ast.TypeExpr, structScope map[string]*ast.StructDecl, name string) (ast.TypeExpr, bool) {
	decl, ok := grammarStructDeclForType(valueType, structScope)
	if !ok || decl == nil {
		return nil, false
	}
	for _, field := range decl.Fields {
		if field.Name == name {
			return field.Type, true
		}
	}
	return nil, false
}
func grammarTupleLiteralShape(valueType ast.TypeExpr) ([]ast.TupleTypeField, bool) {
	tupleType, ok := grammarValueTypeExpr(valueType).(*ast.TupleTypeExpr)
	if !ok || tupleType == nil || len(tupleType.Fields) == 0 {
		return nil, false
	}
	fields := make([]ast.TupleTypeField, 0, len(tupleType.Fields))
	for _, field := range tupleType.Fields {
		if field.Name == "" {
			return nil, false
		}
		fields = append(fields, field)
	}
	return fields, true
}
func grammarTupleFieldTypeExpr(valueType ast.TypeExpr, name string) (ast.TypeExpr, bool) {
	fields, ok := grammarTupleLiteralShape(valueType)
	if !ok {
		return nil, false
	}
	for _, field := range fields {
		if field.Name == name {
			return field.Type, true
		}
	}
	return nil, false
}
func grammarErrorTypeExpr(valueType ast.TypeExpr) ast.TypeExpr {
	if errType, ok := valueType.(*ast.ErrorUnionTypeExpr); ok && errType != nil {
		return errType.Errors
	}
	return nil
}
func grammarMaybeTryExpr(expr ast.Expr, valueType ast.TypeExpr) ast.Expr {
	if expr == nil || grammarErrorTypeExpr(valueType) == nil {
		return expr
	}
	return &ast.TryExpr{Position: expr.Pos(), Value: expr}
}
func grammarCanBlock(pos lexer.Pos, body []ast.Stmt) []ast.Stmt {
	if len(body) == 0 {
		return nil
	}
	return []ast.Stmt{&ast.CanStmt{Position: pos, Permissions: grammarDefaultPermissions(pos), Body: body, SuppressPermissionInference: true}}
}

// grammarAllocScopedCanBlock wraps a generated production body in `in <alloc>:` (when the
// grammarenv declares an allocator) inside the usual can-block. The arena scope makes the whole
// production a place "where a region can be inferred": bare region-backed enum constructors and
// region-polymorphic helper calls inside the production allocate into the parser's arena with no
// explicit threading — the docs/75 inference story applied to lowered grammars.
func grammarAllocScopedCanBlock(pos lexer.Pos, grammarDecl *ast.GrammarDecl, allocExpr ast.Expr, body []ast.Stmt) []ast.Stmt {
	if len(body) == 0 {
		return nil
	}
	// Only an EXPLICIT grammarenv `alloc` directive opts productions into the arena scope —
	// the state-owner fallback would fabricate `in state.owner:` against states that have no
	// such field (and changes nothing for grammars that never allocate nodes).
	if grammarDecl == nil || grammarDecl.AllocExpr == nil {
		allocExpr = nil
	}
	if allocExpr != nil {
		body = []ast.Stmt{&ast.InStoreStmt{Position: pos, Store: cloneHeaderExprAtPos(allocExpr, pos), Body: body}}
	}
	return grammarCanBlock(pos, body)
}
func grammarDefaultPermissions(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{
		{Position: pos, Name: "Abort", Member: "Panic"},
		{Position: pos, Name: "Memory", Member: "Allocate"},
	}
}
func zeroedCastExpr(pos lexer.Pos, target ast.TypeExpr) ast.Expr {
	return &ast.CastExpr{Position: pos, Operand: &ast.ZeroedLit{Position: pos}, Target: target, Origin: ast.CastExprOriginAsSyntax}
}
