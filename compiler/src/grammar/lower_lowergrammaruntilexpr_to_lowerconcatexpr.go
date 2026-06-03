package grammar

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func lowerGrammarUntilExpr(pos lexer.Pos, stops []ast.GrammarTerm) ast.Expr {
	args := make([]ast.Expr, 0, len(stops))
	for _, stop := range stops {
		args = append(args, lowerGrammarUntilStopSurfaceExpr(stop))
	}
	return &ast.CallExpr{Position: pos, Func: &ast.Ident{Position: pos, Name: "until"}, Args: args}
}
func lowerGrammarUntilStopSurfaceExpr(stop ast.GrammarTerm) ast.Expr {
	switch n := stop.(type) {
	case *ast.GrammarTokenTerm:
		return &ast.StringLit{Position: n.Position, Value: n.Value}
	case *ast.GrammarTokenKindTerm:
		return &ast.CallExpr{Position: n.Position, Func: &ast.Ident{Position: n.Position, Name: "token"}, Args: []ast.Expr{grammarTokenKindExpr(n.Position, nil, n.Kind)}}
	case *ast.GrammarCallTerm:
		if !n.Explicit && len(n.Args) == 0 {
			return lowerQualifiedCalleeExpr(n.Position, n.Name)
		}
		return &ast.CallExpr{Position: n.Position, Func: lowerQualifiedCalleeExpr(n.Position, n.Name), Args: append([]ast.Expr(nil), n.Args...)}
	case *ast.GrammarTokenSetRefTerm:
		return &ast.Ident{Position: n.Position, Name: n.Name}
	default:
		return lowerTermExpr(lowerContext{}, stop)
	}
}
func grammarExprEqual(left ast.Expr, right ast.Expr) bool {
	switch l := left.(type) {
	case nil:
		return right == nil
	case *ast.Ident:
		r, ok := right.(*ast.Ident)
		return ok && l.Name == r.Name
	case *ast.IntLit:
		r, ok := right.(*ast.IntLit)
		return ok && l.Value == r.Value
	case *ast.StringLit:
		r, ok := right.(*ast.StringLit)
		return ok && l.Value == r.Value
	case *ast.CharLit:
		r, ok := right.(*ast.CharLit)
		return ok && l.Value == r.Value
	case *ast.BoolLit:
		r, ok := right.(*ast.BoolLit)
		return ok && l.Value == r.Value
	default:
		return false
	}
}
func (ctx *statefulLowerContext) resolveGrammarProductionInfo(term *ast.GrammarCallTerm) (string, ast.GrammarProductionDecl, bool) {
	if term == nil {
		return "", ast.GrammarProductionDecl{}, false
	}
	parts := strings.Split(term.Name, ".")
	if len(parts) == 1 {
		resolved, ok := ctx.productionMap[parts[0]]
		if !ok {
			return "", ast.GrammarProductionDecl{}, false
		}
		return resolved.TryName, resolved.Production, true
	}
	if len(parts) == 2 && parts[0] == ctx.tokenReceiver {
		resolved, ok := ctx.productionMap[parts[1]]
		if !ok {
			return "", ast.GrammarProductionDecl{}, false
		}
		return resolved.TryName, resolved.Production, true
	}
	return "", ast.GrammarProductionDecl{}, false
}
func (ctx *statefulLowerContext) resolveGrammarProductionCall(term *ast.GrammarCallTerm) (string, []ast.Expr, bool) {
	if term == nil {
		return "", nil, false
	}
	parts := strings.Split(term.Name, ".")
	if len(parts) == 1 {
		resolved, ok := ctx.productionMap[parts[0]]
		if !ok {
			return "", nil, false
		}
		return resolved.TryName, grammarCallArgsWithImplicitHeaderArgs(ctx.cursorReceiver, ctx.allocName, resolved.Production, term.Args, term.Position), true
	}
	if len(parts) == 2 && parts[0] == ctx.tokenReceiver {
		resolved, ok := ctx.productionMap[parts[1]]
		if !ok {
			return "", nil, false
		}
		return resolved.TryName, grammarCallArgsWithImplicitHeaderArgs(parts[0], ctx.allocName, resolved.Production, term.Args, term.Position), true
	}
	return "", nil, false
}
func (ctx *statefulLowerContext) resolveGrammarRecoveredPublicCall(term *ast.GrammarCallTerm) (string, []ast.Expr, bool) {
	if term == nil {
		return "", nil, false
	}
	parts := strings.Split(term.Name, ".")
	if len(parts) == 1 {
		resolved, ok := ctx.productionMap[parts[0]]
		if !ok {
			return "", nil, false
		}
		return resolved.Production.Name, grammarCallArgsWithImplicitHeaderArgs(ctx.cursorReceiver, ctx.allocName, resolved.Production, term.Args, term.Position), true
	}
	if len(parts) == 2 && parts[0] == ctx.tokenReceiver {
		resolved, ok := ctx.productionMap[parts[1]]
		if !ok {
			return "", nil, false
		}
		return resolved.Production.Name, grammarCallArgsWithImplicitHeaderArgs(parts[0], ctx.allocName, resolved.Production, term.Args, term.Position), true
	}
	return "", nil, false
}
func grammarCallArgsWithImplicitHeaderArgs(receiverName string, allocName string, production ast.GrammarProductionDecl, args []ast.Expr, pos lexer.Pos) []ast.Expr {
	cloned := append([]ast.Expr(nil), args...)
	if len(production.Params) == 0 {
		return cloned
	}
	implicit := make([]ast.Expr, 0, 2)
	paramIndex := 0
	if receiverName != "" && paramIndex < len(production.Params) && production.Params[paramIndex].Name == receiverName {
		implicit = append(implicit, &ast.Ident{Position: pos, Name: receiverName})
		paramIndex++
	}
	if allocName != "" && paramIndex < len(production.Params) && production.Params[paramIndex].Name == allocName {
		implicit = append(implicit, &ast.Ident{Position: pos, Name: allocName})
		paramIndex++
	}
	if len(implicit) == 0 || len(cloned)+len(implicit) != len(production.Params) {
		return cloned
	}
	out := make([]ast.Expr, 0, len(cloned)+len(implicit))
	out = append(out, implicit...)
	out = append(out, cloned...)
	return out
}
func LowerDecl(decl *ast.GrammarDecl) []*ast.FuncDecl {
	if decl == nil {
		return nil
	}
	decl = normalizeGrammarDeclForLowering(decl)
	funcs := make([]*ast.FuncDecl, 0, len(decl.Productions))
	for _, production := range decl.Productions {
		funcs = append(funcs, LowerProduction(decl, production))
	}
	return funcs
}
func LowerProduction(grammarDecl *ast.GrammarDecl, production ast.GrammarProductionDecl) *ast.FuncDecl {
	if grammarDecl == nil {
		return nil
	}
	production = normalizeGrammarProductionForLowering(grammarDecl, production)
	receiver := grammarReceiverInfoForProduction(grammarDecl, production)
	ctx := lowerContext{
		tokenReceiver:  receiver.tokenReceiver,
		tokenKindType:  grammarDeclTokenKindType(grammarDecl, production.Position),
		tokenKindField: grammarDeclTokenKindField(grammarDecl),
		expectFunc:     grammarDeclExpectFunc(grammarDecl),
		expectKindFunc: grammarDeclExpectKindFunc(grammarDecl),
		eofExpr:        grammarDeclEOFExpr(grammarDecl, production.Position),
		returnType:     production.ReturnType,
		tempCounter:    new(int),
	}
	body := make([]ast.Stmt, 0, len(production.Terms)+1)
	for _, term := range production.Terms {
		body = append(body, lowerTermStmt(ctx, term))
	}
	if production.ReturnType != nil && !grammarTermBlockHasExplicitReturn(production.Terms) {
		body = append(body, &ast.ReturnStmt{Position: production.Position, Value: zeroedCastExpr(production.Position, production.ReturnType)})
	}
	return &ast.FuncDecl{
		Position:         production.Position,
		Name:             production.Name,
		TypeParams:       append([]string(nil), grammarDecl.TypeParams...),
		RefStorageParams: append([]string(nil), grammarDecl.RefStorageParams...),
		RegionParams:     append([]string(nil), grammarDecl.RegionParams...),
		PermissionParams: append([]string(nil), grammarDecl.PermissionParams...),
		GenericParams:    append([]ast.GenericParam(nil), grammarDecl.GenericParams...),
		Params:           append([]ast.ParamDecl(nil), production.Params...),
		ReturnType:       production.ReturnType,
		Body:             body,
	}
}
func grammarTermBlockHasExplicitReturn(terms []ast.GrammarTerm) bool {
	for _, term := range terms {
		if _, ok := term.(*ast.GrammarReturnTerm); ok {
			return true
		}
	}
	return false
}
func lowerReturnedGrammarTerm(ctx lowerContext, term ast.GrammarTerm) ([]ast.Stmt, ast.Expr) {
	switch n := term.(type) {
	case *ast.GrammarSeqTerm:
		if n == nil {
			return nil, nil
		}
		if len(n.Terms) == 0 {
			return nil, &ast.BoolLit{Position: n.Position, Value: true}
		}
		body := make([]ast.Stmt, 0, len(n.Terms))
		for _, child := range n.Terms[:len(n.Terms)-1] {
			body = append(body, lowerTermStmt(ctx, child))
		}
		tailBody, tailValue := lowerReturnedGrammarTerm(ctx, n.Terms[len(n.Terms)-1])
		body = append(body, tailBody...)
		return body, tailValue
	case *ast.GrammarBindTerm:
		body, value := lowerReturnedGrammarTerm(ctx, n.Term)
		body = append(body, &ast.VarDeclStmt{Position: n.Position, Name: n.Name, Value: value})
		return body, &ast.Ident{Position: n.Position, Name: n.Name}
	case *ast.GrammarAssignTerm:
		body, value := lowerReturnedGrammarTerm(ctx, n.Term)
		body = append(body, &ast.AssignStmt{Position: n.Position, Target: &ast.Ident{Position: n.Position, Name: n.Name}, Value: value})
		return body, &ast.Ident{Position: n.Position, Name: n.Name}
	case *ast.GrammarReturnTerm:
		if n == nil {
			return nil, nil
		}
		return lowerReturnedGrammarTerm(ctx, n.Term)
	default:
		return nil, lowerTermExpr(ctx, term)
	}
}
func lowerExplicitReturnStmt(ctx lowerContext, term *ast.GrammarReturnTerm) ast.Stmt {
	if term == nil {
		return &ast.ReturnStmt{}
	}
	body, value := lowerReturnedGrammarTerm(ctx, term.Term)
	body = append(body, &ast.ReturnStmt{Position: term.Position, Value: value})
	if len(body) == 1 {
		return body[0]
	}
	return &ast.CanStmt{Position: term.Position, Permissions: grammarDefaultPermissions(term.Position), Body: body, SuppressPermissionInference: true}
}
func grammarTokenReceiverName(params []ast.ParamDecl) string {
	for _, param := range params {
		if param.Name == "state" {
			return param.Name
		}
	}
	return ""
}
func grammarStateReceiver(params []ast.ParamDecl) grammarStateReceiverInfo {
	for _, param := range params {
		if param.Name == "state" {
			return grammarStateReceiverInfo{cursorReceiver: param.Name, cursorField: "cursor", tokenReceiver: param.Name}
		}
	}
	for _, param := range params {
		if param.Name == "self" || param.Name == "parser" {
			return grammarStateReceiverInfo{cursorReceiver: param.Name, cursorField: "pos"}
		}
	}
	return grammarStateReceiverInfo{}
}
func lowerTermStmt(ctx lowerContext, term ast.GrammarTerm) ast.Stmt {
	switch n := term.(type) {
	case *ast.GrammarPassTerm:
		return &ast.PassStmt{Position: n.Position}
	case *ast.GrammarBindTerm:
		return &ast.VarDeclStmt{Position: n.Position, Name: n.Name, Value: lowerTermExpr(ctx, n.Term)}
	case *ast.GrammarAssignTerm:
		return &ast.AssignStmt{Position: n.Position, Target: &ast.Ident{Position: n.Position, Name: n.Name}, Value: lowerTermExpr(ctx, n.Term)}
	case *ast.GrammarCutTerm:
		return &ast.PassStmt{Position: n.Position}
	case *ast.GrammarReturnTerm:
		return lowerExplicitReturnStmt(ctx, n)
	default:
		return &ast.ExprStmt{Position: term.Pos(), Expr: lowerTermExpr(ctx, term)}
	}
}
func lowerTermExpr(ctx lowerContext, term ast.GrammarTerm) ast.Expr {
	switch n := term.(type) {
	case *ast.GrammarTokenTerm:
		funcExpr := grammarExpectFuncExpr(n.Position, ctx.tokenReceiver, ctx.expectFunc)
		return &ast.CallExpr{
			Position: n.Position,
			Func:     funcExpr,
			Args:     []ast.Expr{&ast.StringLit{Position: n.Position, Value: n.Value}},
		}
	case *ast.GrammarTokenKindTerm:
		return &ast.CallExpr{
			Position: n.Position,
			Func:     grammarExpectFuncExpr(n.Position, ctx.tokenReceiver, ctx.expectKindFunc),
			Args:     []ast.Expr{grammarTokenKindExpr(n.Position, ctx.tokenKindType, n.Kind)},
		}
	case *ast.GrammarCallTerm:
		if kindExpr, ok := grammarTokenKindMatcher(n); ok {
			return &ast.CallExpr{
				Position: n.Position,
				Func:     grammarExpectFuncExpr(n.Position, ctx.tokenReceiver, ctx.expectKindFunc),
				Args:     []ast.Expr{kindExpr},
			}
		}
		if !n.Explicit && len(n.Args) == 0 {
			return lowerQualifiedCalleeExpr(n.Position, n.Name)
		}
		return &ast.CallExpr{
			Position: n.Position,
			Func:     lowerQualifiedCalleeExpr(n.Position, n.Name),
			Args:     append([]ast.Expr(nil), n.Args...),
		}
	case *ast.GrammarChoiceTerm:
		return lowerChoiceExpr(ctx, n.Position, n.Options)
	case *ast.GrammarWhenTerm:
		cond := n.Cond
		if n.TokenKindGate != "" {
			cond = grammarTokenKindMatchExpr(n.Position, currentTokenExpr(ctx.tokenReceiver, "", n.Position), ctx.tokenKindField, grammarTokenKindExpr(n.Position, ctx.tokenKindType, n.TokenKindGate))
		}
		return &ast.TernaryExpr{Position: n.Position, Value: lowerTermExpr(ctx, n.Then), Cond: cond, Alt: lowerTermExpr(ctx, n.Else)}
	case *ast.GrammarRequiredTerm:
		return lowerTermExpr(ctx, n.Term)
	case *ast.GrammarDelimitedTerm:
		return lowerTermExpr(ctx, n.Body)
	case *ast.GrammarSeqTerm:
		if len(n.Terms) == 0 {
			return &ast.BoolLit{Position: n.Position, Value: true}
		}
		return lowerTermExpr(ctx, n.Terms[len(n.Terms)-1])
	case *ast.GrammarLookaheadTerm:
		return lowerTermExpr(ctx, n.Term)
	case *ast.GrammarExprTerm:
		return lowerTypedGrammarExpr(ctx, n.Position, n.Type, n.Expr)
	case *ast.GrammarSingletonTerm:
		return lowerSingletonExpr(ctx, n)
	case *ast.GrammarEmptyTerm:
		return lowerEmptyExpr(ctx, n)
	case *ast.GrammarConcatTerm:
		return lowerConcatExpr(ctx, n)
	case *ast.GrammarRecoverTerm:
		return lowerTermExpr(ctx, n.Term)
	case *ast.GrammarGuardTerm:
		return n.Cond
	case *ast.GrammarAttemptTerm:
		return n.Expr
	case *ast.GrammarCutTerm:
		return &ast.BoolLit{Position: n.Position, Value: true}
	case *ast.GrammarOptionalTerm:
		return &ast.CallExpr{Position: n.Position, Func: &ast.Ident{Position: n.Position, Name: "optional"}, Args: []ast.Expr{lowerTermExpr(ctx, n.Term)}}
	case *ast.GrammarListTerm:
		args := []ast.Expr{lowerTermExpr(ctx, n.Elem)}
		if n.Separator != nil {
			args = append(args, lowerTermExpr(ctx, n.Separator))
		} else {
			args = append(args, &ast.ZeroedLit{Position: n.Position})
		}
		if len(n.Until) != 0 {
			args = append(args, lowerGrammarUntilExpr(n.Position, n.Until))
		}
		return &ast.CallExpr{Position: n.Position, Func: &ast.Ident{Position: n.Position, Name: "list"}, Args: args}
	case *ast.GrammarRepeatTerm:
		args := []ast.Expr{lowerTermExpr(ctx, n.Elem)}
		if len(n.Until) != 0 {
			args = append(args, lowerGrammarUntilExpr(n.Position, n.Until))
		}
		return &ast.CallExpr{Position: n.Position, Func: &ast.Ident{Position: n.Position, Name: "repeat"}, Args: args}
	case *ast.GrammarFlatRepeatTerm:
		return lowerTermExpr(ctx, n.Elem)
	case *ast.GrammarWhileTerm:
		return lowerTermExpr(ctx, n.Elem)
	case *ast.GrammarSeparatedTerm:
		args := []ast.Expr{lowerTermExpr(ctx, n.Elem), lowerTermExpr(ctx, n.Separator)}
		if len(n.Until) != 0 {
			args = append(args, lowerGrammarUntilExpr(n.Position, n.Until))
		}
		return &ast.CallExpr{Position: n.Position, Func: &ast.Ident{Position: n.Position, Name: "separated"}, Args: args}
	case *ast.GrammarSuffixTerm:
		return lowerTermExpr(ctx, n.Seed)
	case *ast.GrammarPostfixTerm:
		return lowerTermExpr(ctx, n.Seed)
	case *ast.GrammarPrecedenceTerm:
		return lowerTermExpr(ctx, n.Seed)
	case *ast.GrammarBindTerm:
		return lowerTermExpr(ctx, n.Term)
	case *ast.GrammarPassTerm:
		return &ast.ZeroedLit{Position: n.Position}
	case *ast.GrammarReturnTerm:
		return lowerTermExpr(ctx, n.Term)
	default:
		return &ast.Ident{Position: term.Pos(), Name: "<invalid_grammar_term>"}
	}
}
func lowerQualifiedCalleeExpr(pos lexer.Pos, name string) ast.Expr {
	parts := strings.Split(name, ".")
	if len(parts) == 0 {
		return &ast.Ident{Position: pos, Name: name}
	}
	var expr ast.Expr = &ast.Ident{Position: pos, Name: parts[0]}
	for _, part := range parts[1:] {
		expr = &ast.FieldExpr{Position: pos, Object: expr, Field: part}
	}
	return expr
}
func lowerChoiceExpr(ctx lowerContext, pos lexer.Pos, options []ast.GrammarTerm) ast.Expr {
	if len(options) == 0 {
		return &ast.ZeroedLit{Position: pos}
	}
	if len(options) == 1 {
		return lowerTermExpr(ctx, options[0])
	}
	return &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: "choice"},
		Args: []ast.Expr{
			lowerTermExpr(ctx, options[0]),
			lowerChoiceExpr(ctx, pos, options[1:]),
		},
	}
}
func lowerTypedGrammarExpr(ctx lowerContext, pos lexer.Pos, typ ast.TypeExpr, expr ast.Expr) ast.Expr {
	if wrapped, ok := lowerAllocatingGrammarExpr(ctx, pos, typ, expr); ok {
		return wrapped
	}
	if typ == nil {
		return expr
	}
	valueName := ctx.fresh("expr_value")
	return &ast.ExprBlock{
		Position: pos,
		Stmts: []ast.Stmt{
			&ast.VarDeclStmt{Position: pos, Name: valueName, Type: typ, Value: expr},
		},
		Value: &ast.Ident{Position: pos, Name: valueName},
	}
}
func lowerAllocatingGrammarExpr(ctx lowerContext, pos lexer.Pos, typ ast.TypeExpr, expr ast.Expr) (ast.Expr, bool) {
	if ctx.allocExpr == nil {
		return nil, false
	}
	if _, ok := expr.(*ast.ListComprehensionExpr); !ok {
		return nil, false
	}
	valueType := typ
	if valueType == nil {
		valueType = ctx.returnType
	}
	if valueType == nil {
		return nil, false
	}
	valueName := ctx.fresh("expr_value")
	return &ast.ExprBlock{
		Position: pos,
		Stmts: []ast.Stmt{
			&ast.VarDeclStmt{Position: pos, Name: valueName, Type: valueType, Mutable: true, Value: &ast.ZeroedLit{Position: pos}},
			&ast.InStoreStmt{
				Position: pos,
				Store:    ctx.allocExpr,
				Body: []ast.Stmt{
					&ast.AssignStmt{Position: pos, Target: &ast.Ident{Position: pos, Name: valueName}, Value: expr},
				},
			},
		},
		Value: &ast.Ident{Position: pos, Name: valueName},
	}, true
}
func lowerSingletonExpr(ctx lowerContext, term *ast.GrammarSingletonTerm) ast.Expr {
	elemType := grammarListElementType(term.Position, term.Type, ctx.returnType)
	resultType := listTypeExpr(term.Position, elemType)
	resultName := ctx.fresh("singleton_value")
	return &ast.ExprBlock{
		Position: term.Position,
		Stmts: []ast.Stmt{
			&ast.VarDeclStmt{Position: term.Position, Name: resultName, Mutable: true, Type: resultType, Value: &ast.ListLitExpr{Position: term.Position}},
			listPushStmt(term.Position, resultName, term.Value),
		},
		Value: &ast.Ident{Position: term.Position, Name: resultName},
	}
}
func lowerEmptyExpr(ctx lowerContext, term *ast.GrammarEmptyTerm) ast.Expr {
	elemType := grammarListElementType(term.Position, term.Type, ctx.returnType)
	resultType := listTypeExpr(term.Position, elemType)
	valueName := ctx.fresh("empty_value")
	return &ast.ExprBlock{
		Position: term.Position,
		Stmts: []ast.Stmt{
			&ast.VarDeclStmt{Position: term.Position, Name: valueName, Type: resultType, Value: &ast.ListLitExpr{Position: term.Position}},
		},
		Value: &ast.Ident{Position: term.Position, Name: valueName},
	}
}
func lowerConcatExpr(ctx lowerContext, term *ast.GrammarConcatTerm) ast.Expr {
	if term == nil || len(term.Terms) == 0 {
		return &ast.ListLitExpr{Position: lexer.Pos{}}
	}
	resultType := inferLowerContextTermType(term, ctx.returnType)
	if resultType == nil {
		resultType = ctx.returnType
	}
	resultName := ctx.fresh("concat_value")
	stmts := make([]ast.Stmt, 0, len(term.Terms)*3+1)
	stmts = append(stmts, &ast.VarDeclStmt{Position: term.Position, Name: resultName, Mutable: true, Type: resultType, Value: &ast.ListLitExpr{Position: term.Position}})
	for _, child := range term.Terms {
		groupName := ctx.fresh("concat_group")
		groupType := inferLowerContextTermType(child, resultType)
		if groupType == nil {
			groupType = resultType
		}
		stmts = append(stmts, &ast.VarDeclStmt{Position: child.Pos(), Name: groupName, Type: groupType, Value: lowerTermExpr(ctx, child)})
		stmts = append(stmts, listPushIndexedItemsExprStmts(ctx, child.Pos(), resultName, groupName)...)
	}
	return &ast.ExprBlock{Position: term.Position, Stmts: stmts, Value: &ast.Ident{Position: term.Position, Name: resultName}}
}
