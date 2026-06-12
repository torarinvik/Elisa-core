package grammar

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func rewriteGrammarTermListTokenAliases(terms []ast.GrammarTerm, aliases map[string]string) []ast.GrammarTerm {
	if len(terms) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarTerm, 0, len(terms))
	for _, term := range terms {
		rewritten = append(rewritten, rewriteGrammarTermTokenAliases(term, aliases))
	}
	return rewritten
}
func rewriteGrammarBindingsTokenAliases(bindings []*ast.GrammarBindTerm, aliases map[string]string) []*ast.GrammarBindTerm {
	if len(bindings) == 0 {
		return nil
	}
	rewritten := make([]*ast.GrammarBindTerm, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		rewritten = append(rewritten, &ast.GrammarBindTerm{Position: binding.Position, Name: binding.Name, Term: rewriteGrammarTermTokenAliases(binding.Term, aliases)})
	}
	return rewritten
}
func rewriteGrammarPostfixArmsTokenAliases(arms []ast.GrammarPostfixArm, aliases map[string]string) []ast.GrammarPostfixArm {
	if len(arms) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarPostfixArm, 0, len(arms))
	for _, arm := range arms {
		rewritten = append(rewritten, ast.GrammarPostfixArm{Position: arm.Position, OpName: arm.OpName, Op: rewriteGrammarTermTokenAliases(arm.Op, aliases), Block: arm.Block, Bindings: rewriteGrammarBindingsTokenAliases(arm.Bindings, aliases), Value: arm.Value})
	}
	return rewritten
}
func rewriteGrammarPrecedenceArmsTokenAliases(arms []ast.GrammarPrecedenceArm, aliases map[string]string) []ast.GrammarPrecedenceArm {
	if len(arms) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarPrecedenceArm, 0, len(arms))
	for _, arm := range arms {
		rewritten = append(rewritten, ast.GrammarPrecedenceArm{Position: arm.Position, OpName: arm.OpName, Op: rewriteGrammarTermTokenAliases(arm.Op, aliases), Block: arm.Block, Bindings: rewriteGrammarBindingsTokenAliases(arm.Bindings, aliases), Value: arm.Value})
	}
	return rewritten
}
func rewriteGrammarPrecedenceLevelsTokenAliases(levels []ast.GrammarPrecedenceLevel, aliases map[string]string) []ast.GrammarPrecedenceLevel {
	if len(levels) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarPrecedenceLevel, 0, len(levels))
	for _, level := range levels {
		rewritten = append(rewritten, ast.GrammarPrecedenceLevel{Position: level.Position, Assoc: level.Assoc, Name: level.Name, LeftName: level.LeftName, Seed: rewriteGrammarTermTokenAliases(level.Seed, aliases), Arms: rewriteGrammarPrecedenceArmsTokenAliases(level.Arms, aliases)})
	}
	return rewritten
}
func rewriteGrammarTermTokenAliases(term ast.GrammarTerm, aliases map[string]string) ast.GrammarTerm {
	if len(aliases) == 0 || term == nil {
		return term
	}
	switch n := term.(type) {
	case *ast.GrammarTokenTerm:
		if kind, ok := aliases[n.Value]; ok {
			return &ast.GrammarTokenKindTerm{Position: n.Position, Kind: kind}
		}
		return n
	case *ast.GrammarBindTerm:
		return &ast.GrammarBindTerm{Position: n.Position, Name: n.Name, Term: rewriteGrammarTermTokenAliases(n.Term, aliases)}
	case *ast.GrammarAssignTerm:
		return &ast.GrammarAssignTerm{Position: n.Position, Name: n.Name, Term: rewriteGrammarTermTokenAliases(n.Term, aliases)}
	case *ast.GrammarReturnTerm:
		return &ast.GrammarReturnTerm{Position: n.Position, Term: rewriteGrammarTermTokenAliases(n.Term, aliases)}
	case *ast.GrammarChoiceTerm:
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: rewriteGrammarTermListTokenAliases(n.Options, aliases)}
	case *ast.GrammarOptionalTerm:
		return &ast.GrammarOptionalTerm{Position: n.Position, Term: rewriteGrammarTermTokenAliases(n.Term, aliases)}
	case *ast.GrammarWhenTerm:
		return &ast.GrammarWhenTerm{Position: n.Position, Cond: n.Cond, TokenKindGate: n.TokenKindGate, Then: rewriteGrammarTermTokenAliases(n.Then, aliases), Else: rewriteGrammarTermTokenAliases(n.Else, aliases)}
	case *ast.GrammarRecoverTerm:
		return &ast.GrammarRecoverTerm{Position: n.Position, Term: rewriteGrammarTermTokenAliases(n.Term, aliases), RecoverPolicy: n.RecoverPolicy, RecoverMsg: n.RecoverMsg, RecoverUntil: rewriteGrammarTermListTokenAliases(n.RecoverUntil, aliases), RecoverValue: n.RecoverValue}
	case *ast.GrammarRequiredTerm:
		return &ast.GrammarRequiredTerm{Position: n.Position, Term: rewriteGrammarTermTokenAliases(n.Term, aliases), Message: n.Message}
	case *ast.GrammarDelimitedTerm:
		return &ast.GrammarDelimitedTerm{Position: n.Position, Open: rewriteGrammarTermTokenAliases(n.Open, aliases), Body: rewriteGrammarTermTokenAliases(n.Body, aliases), Close: rewriteGrammarTermTokenAliases(n.Close, aliases), Message: n.Message}
	case *ast.GrammarSeqTerm:
		return &ast.GrammarSeqTerm{Position: n.Position, Terms: rewriteGrammarTermListTokenAliases(n.Terms, aliases)}
	case *ast.GrammarLookaheadTerm:
		return &ast.GrammarLookaheadTerm{Position: n.Position, Term: rewriteGrammarTermTokenAliases(n.Term, aliases)}
	case *ast.GrammarSingletonTerm:
		return &ast.GrammarSingletonTerm{Position: n.Position, Type: n.Type, Value: n.Value}
	case *ast.GrammarEmptyTerm:
		return &ast.GrammarEmptyTerm{Position: n.Position, Type: n.Type}
	case *ast.GrammarConcatTerm:
		return &ast.GrammarConcatTerm{Position: n.Position, Terms: rewriteGrammarTermListTokenAliases(n.Terms, aliases)}
	case *ast.GrammarListTerm:
		return &ast.GrammarListTerm{Position: n.Position, Elem: rewriteGrammarTermTokenAliases(n.Elem, aliases), Separator: rewriteGrammarTermTokenAliases(n.Separator, aliases), Until: rewriteGrammarTermListTokenAliases(n.Until, aliases)}
	case *ast.GrammarRepeatTerm:
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: rewriteGrammarTermTokenAliases(n.Elem, aliases), Until: rewriteGrammarTermListTokenAliases(n.Until, aliases), MinOne: n.MinOne}
	case *ast.GrammarFlatRepeatTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: rewriteGrammarTermTokenAliases(n.Elem, aliases), Until: rewriteGrammarTermListTokenAliases(n.Until, aliases)}
	case *ast.GrammarWhileTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: rewriteGrammarTermTokenAliases(n.Elem, aliases), Until: rewriteGrammarTermListTokenAliases(n.Until, aliases)}
	case *ast.GrammarSeparatedTerm:
		return &ast.GrammarSeparatedTerm{Position: n.Position, Elem: rewriteGrammarTermTokenAliases(n.Elem, aliases), Separator: rewriteGrammarTermTokenAliases(n.Separator, aliases), Until: rewriteGrammarTermListTokenAliases(n.Until, aliases)}
	case *ast.GrammarSuffixTerm:
		return &ast.GrammarSuffixTerm{Position: n.Position, LeftName: n.LeftName, Seed: rewriteGrammarTermTokenAliases(n.Seed, aliases), Arms: rewriteGrammarPostfixArmsTokenAliases(n.Arms, aliases)}
	case *ast.GrammarPostfixTerm:
		return &ast.GrammarPostfixTerm{Position: n.Position, LeftName: n.LeftName, Seed: rewriteGrammarTermTokenAliases(n.Seed, aliases), Arms: rewriteGrammarPostfixArmsTokenAliases(n.Arms, aliases)}
	case *ast.GrammarPrecedenceTerm:
		return &ast.GrammarPrecedenceTerm{Position: n.Position, Assoc: n.Assoc, Result: n.Result, Levels: rewriteGrammarPrecedenceLevelsTokenAliases(n.Levels, aliases), LeftName: n.LeftName, Seed: rewriteGrammarTermTokenAliases(n.Seed, aliases), Arms: rewriteGrammarPrecedenceArmsTokenAliases(n.Arms, aliases)}
	default:
		return term
	}
}
func expandAugmentedGrammarProductions(productions []ast.GrammarProductionDecl) []ast.GrammarProductionDecl {
	if len(productions) == 0 {
		return nil
	}
	appendByName := make(map[string][]ast.GrammarProductionDecl)
	baseList := make([]ast.GrammarProductionDecl, 0, len(productions))
	for _, production := range productions {
		if production.Append {
			appendByName[production.Name] = append(appendByName[production.Name], production)
			continue
		}
		baseList = append(baseList, production)
	}
	if len(appendByName) == 0 {
		return append([]ast.GrammarProductionDecl(nil), productions...)
	}
	out := make([]ast.GrammarProductionDecl, 0, len(productions)*2)
	for _, base := range baseList {
		appends := appendByName[base.Name]
		if len(appends) == 0 {
			out = append(out, base)
			continue
		}
		merged, helpers := buildAugmentedGrammarProduction(base, appends)
		out = append(out, merged)
		out = append(out, helpers...)
	}
	return out
}
func buildAugmentedGrammarProduction(base ast.GrammarProductionDecl, appends []ast.GrammarProductionDecl) (ast.GrammarProductionDecl, []ast.GrammarProductionDecl) {
	helperBaseName := grammarAugmentHelperProductionName(base.Name, "base", 0)
	helpers := make([]ast.GrammarProductionDecl, 0, len(appends)+1)
	helpers = append(helpers, ast.GrammarProductionDecl{Position: base.Position, Name: helperBaseName, HasParamList: base.HasParamList || len(base.Params) != 0, Params: append([]ast.ParamDecl(nil), base.Params...), ReturnType: base.ReturnType, Channels: append([]ast.GrammarChannelDecl(nil), base.Channels...), Terms: append([]ast.GrammarTerm(nil), base.Terms...)})
	options := []ast.GrammarTerm{grammarAugmentHelperCall(base.Position, helperBaseName, base.Params)}
	for index, appendProduction := range appends {
		helperName := grammarAugmentHelperProductionName(base.Name, "append", index+1)
		helpers = append(helpers, ast.GrammarProductionDecl{Position: appendProduction.Position, Name: helperName, HasParamList: base.HasParamList || len(base.Params) != 0, Params: append([]ast.ParamDecl(nil), base.Params...), ReturnType: base.ReturnType, Channels: append([]ast.GrammarChannelDecl(nil), appendProduction.Channels...), Terms: append([]ast.GrammarTerm(nil), appendProduction.Terms...)})
		options = append(options, grammarAugmentHelperCall(appendProduction.Position, helperName, base.Params))
	}
	merged := base
	if base.ReturnType != nil {
		resultName := grammarAugmentResultName(base.Name)
		merged.Terms = []ast.GrammarTerm{
			&ast.GrammarBindTerm{Position: base.Position, Name: resultName, Term: &ast.GrammarChoiceTerm{Position: base.Position, Options: options}},
			&ast.GrammarReturnTerm{Position: base.Position, Term: &ast.GrammarExprTerm{Position: base.Position, Expr: &ast.Ident{Position: base.Position, Name: resultName}}},
		}
	} else {
		merged.Terms = []ast.GrammarTerm{&ast.GrammarChoiceTerm{Position: base.Position, Options: options}}
	}
	return merged, helpers
}
func grammarAugmentHelperProductionName(baseName string, kind string, index int) string {
	return "__grammar_augment_" + sanitizeGrammarHelperName(baseName) + "_" + kind + "_" + itoa(index)
}
func grammarAugmentResultName(baseName string) string {
	return "__grammar_augment_result_" + sanitizeGrammarHelperName(baseName)
}
func grammarAugmentHelperCall(pos lexer.Pos, helperName string, params []ast.ParamDecl) ast.GrammarTerm {
	return &ast.GrammarCallTerm{Position: pos, Name: helperName, Explicit: true, Args: grammarProductionParamArgs(params)}
}
func normalizeGrammarProductionForLowering(grammarDecl *ast.GrammarDecl, production ast.GrammarProductionDecl) ast.GrammarProductionDecl {
	if grammarDecl == nil {
		return production
	}
	params := append([]ast.ParamDecl(nil), production.Params...)
	if explicit := grammarStateReceiver(production.Params); explicit.cursorReceiver == "" {
		headerParam, _, ok := grammarHeaderStateParam(grammarDecl, production.Position)
		if ok && !grammarParamListHasName(params, headerParam.Name) {
			params = append([]ast.ParamDecl{headerParam}, params...)
		}
	}
	if allocParam, ok := grammarHeaderAllocParam(grammarDecl, production.Position); ok && !grammarParamListHasName(params, allocParam.Name) {
		insertAt := 0
		if receiver := grammarStateReceiver(params); receiver.cursorReceiver != "" && len(params) != 0 && params[0].Name == receiver.cursorReceiver {
			insertAt = 1
		}
		params = append(params[:insertAt], append([]ast.ParamDecl{allocParam}, params[insertAt:]...)...)
	}
	production.Params = params
	if errorType := grammarHeaderErrorSetType(grammarDecl); errorType != nil && production.ReturnType != nil && grammarErrorTypeExpr(production.ReturnType) == nil {
		production.ReturnType = &ast.ErrorUnionTypeExpr{Position: production.ReturnType.Pos(), Value: production.ReturnType, Errors: errorType}
	}
	return production
}
func grammarParamListHasName(params []ast.ParamDecl, name string) bool {
	for _, param := range params {
		if param.Name == name {
			return true
		}
	}
	return false
}
func grammarHeaderErrorSetType(grammarDecl *ast.GrammarDecl) ast.TypeExpr {
	if grammarDecl == nil || grammarDecl.ErrorType == nil {
		return nil
	}
	switch n := grammarDecl.ErrorType.(type) {
	case *ast.ErrorSetExpr:
		tags := append([]ast.ErrorTagExpr(nil), n.Tags...)
		return &ast.ErrorSetExpr{Position: n.Position, Tags: tags, HasEllipsis: n.HasEllipsis}
	case *ast.NamedType:
		return &ast.ErrorSetExpr{Position: n.Position, Tags: []ast.ErrorTagExpr{{Position: n.Position, SetName: n.Name}}}
	default:
		return grammarDecl.ErrorType
	}
}
func arenaParamType(pos lexer.Pos) ast.TypeExpr {
	return &ast.MutableType{
		Position: pos,
		Elem: &ast.RefType{
			Position: pos,
			Elem:     &ast.NamedType{Position: pos, Name: "Arena"},
			State:    ast.RefStateNonNull,
			Storage:  ast.RefStorageAny,
		},
	}
}
func grammarHeaderAllocParam(grammarDecl *ast.GrammarDecl, pos lexer.Pos) (ast.ParamDecl, bool) {
	if grammarDecl == nil || grammarDecl.AllocExpr == nil {
		return ast.ParamDecl{}, false
	}
	ident, ok := grammarDecl.AllocExpr.(*ast.Ident)
	if !ok || ident.Name == "" {
		return ast.ParamDecl{}, false
	}
	return ast.ParamDecl{Position: pos, Name: ident.Name, Type: arenaParamType(pos)}, true
}
func grammarAllocNameForProduction(grammarDecl *ast.GrammarDecl, production ast.GrammarProductionDecl) string {
	if grammarDecl == nil || grammarDecl.AllocExpr == nil {
		return ""
	}
	ident, ok := grammarDecl.AllocExpr.(*ast.Ident)
	if !ok {
		return ""
	}
	for _, param := range production.Params {
		if param.Name == ident.Name {
			return ident.Name
		}
	}
	return ""
}
func cloneHeaderExprAtPos(expr ast.Expr, pos lexer.Pos) ast.Expr {
	switch n := expr.(type) {
	case *ast.Ident:
		return &ast.Ident{Position: pos, Name: n.Name}
	case *ast.FieldExpr:
		return &ast.FieldExpr{Position: pos, Object: cloneHeaderExprAtPos(n.Object, pos), Field: n.Field}
	default:
		return expr
	}
}
func grammarAllocExprForProduction(grammarDecl *ast.GrammarDecl, production ast.GrammarProductionDecl, receiver grammarStateReceiverInfo, pos lexer.Pos) ast.Expr {
	if name := grammarAllocNameForProduction(grammarDecl, production); name != "" {
		return &ast.Ident{Position: pos, Name: name}
	}
	if grammarDecl != nil && grammarDecl.AllocExpr != nil {
		return cloneHeaderExprAtPos(grammarDecl.AllocExpr, pos)
	}
	if receiver.tokenReceiver != "" {
		return stateOwnerExpr(receiver.tokenReceiver, pos)
	}
	return nil
}
func grammarDeclTokenType(grammarDecl *ast.GrammarDecl, pos lexer.Pos) ast.TypeExpr {
	if grammarDecl != nil && grammarDecl.OverType != nil {
		return grammarDecl.OverType
	}
	return builtinTypeExpr(pos, "Token")
}
func grammarDeclTokenKindType(grammarDecl *ast.GrammarDecl, pos lexer.Pos) ast.TypeExpr {
	if grammarDecl != nil && grammarDecl.TokenKindType != nil {
		return grammarDecl.TokenKindType
	}
	if grammarDecl != nil && grammarDecl.TokenEnumName != "" {
		return &ast.NamedType{Position: pos, Name: grammarDecl.TokenEnumName}
	}
	return builtinTypeExpr(pos, "TokenKind")
}
func grammarDeclEOFExpr(grammarDecl *ast.GrammarDecl, pos lexer.Pos) ast.Expr {
	if grammarDecl != nil && grammarDecl.EOFExpr != nil {
		return cloneHeaderExprAtPos(grammarDecl.EOFExpr, pos)
	}
	if grammarDecl != nil && grammarDecl.TokenEnumName != "" {
		return &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: grammarDecl.TokenEnumName}, Field: "EOF"}
	}
	return &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: "TokenKind"}, Field: "EOF"}
}
func grammarDeclTokenLookupFunc(grammarDecl *ast.GrammarDecl) string {
	if grammarDecl != nil && grammarDecl.TokenLookupFunc != "" {
		return grammarDecl.TokenLookupFunc
	}
	return "token_kind_for_text"
}
func grammarDeclTokenKindField(grammarDecl *ast.GrammarDecl) string {
	if grammarDecl != nil && grammarDecl.TokenKindField != "" {
		return grammarDecl.TokenKindField
	}
	return "kind"
}
func grammarDeclCurrentFunc(grammarDecl *ast.GrammarDecl) string {
	if grammarDecl != nil && grammarDecl.CurrentFunc != "" {
		return grammarDecl.CurrentFunc
	}
	return "current_token"
}
func grammarDeclAdvanceFunc(grammarDecl *ast.GrammarDecl) string {
	if grammarDecl != nil && grammarDecl.AdvanceFunc != "" {
		return grammarDecl.AdvanceFunc
	}
	return "advance_token"
}
func grammarDeclExpectFunc(grammarDecl *ast.GrammarDecl) string {
	if grammarDecl != nil && grammarDecl.ExpectFunc != "" {
		return grammarDecl.ExpectFunc
	}
	return "expect"
}
func grammarDeclExpectKindFunc(grammarDecl *ast.GrammarDecl) string {
	if grammarDecl != nil && grammarDecl.ExpectKindFunc != "" {
		return grammarDecl.ExpectKindFunc
	}
	return "expect_kind"
}
func grammarDeclRecordErrorFunc(grammarDecl *ast.GrammarDecl) string {
	if grammarDecl != nil && grammarDecl.RecordErrorFunc != "" {
		return grammarDecl.RecordErrorFunc
	}
	return "record_parse_error"
}
func desugarNamedPrecedenceProduction(grammarName string, production ast.GrammarProductionDecl) (ast.GrammarProductionDecl, []ast.GrammarProductionDecl) {
	if len(production.Terms) == 0 {
		return production, nil
	}
	counter := 0
	helpers := make([]ast.GrammarProductionDecl, 0)
	terms := make([]ast.GrammarTerm, 0, len(production.Terms))
	for _, term := range production.Terms {
		rewritten, extra := desugarNamedPrecedenceTerm(grammarName, production, term, &counter)
		terms = append(terms, rewritten)
		helpers = append(helpers, extra...)
	}
	production.Terms = terms
	return production, helpers
}
func desugarNamedPrecedenceTerm(grammarName string, production ast.GrammarProductionDecl, term ast.GrammarTerm, counter *int) (ast.GrammarTerm, []ast.GrammarProductionDecl) {
	switch n := term.(type) {
	case *ast.GrammarBindTerm:
		if precedence, ok := n.Term.(*ast.GrammarPrecedenceTerm); ok && precedence != nil && precedence.Assoc != "" && len(precedence.Levels) == 0 {
			rewritten, helpers := desugarAssociativeInlinePrecedenceTerm(grammarName, production, precedence, counter)
			return &ast.GrammarBindTerm{Position: n.Position, Name: n.Name, Term: rewritten}, helpers
		}
		rewritten, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Term, counter)
		return &ast.GrammarBindTerm{Position: n.Position, Name: n.Name, Term: rewritten}, helpers
	case *ast.GrammarAssignTerm:
		if precedence, ok := n.Term.(*ast.GrammarPrecedenceTerm); ok && precedence != nil && precedence.Assoc != "" && len(precedence.Levels) == 0 {
			rewritten, helpers := desugarAssociativeInlinePrecedenceTerm(grammarName, production, precedence, counter)
			return &ast.GrammarAssignTerm{Position: n.Position, Name: n.Name, Term: rewritten}, helpers
		}
		rewritten, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Term, counter)
		return &ast.GrammarAssignTerm{Position: n.Position, Name: n.Name, Term: rewritten}, helpers
	case *ast.GrammarReturnTerm:
		rewritten, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Term, counter)
		return &ast.GrammarReturnTerm{Position: n.Position, Term: rewritten}, helpers
	case *ast.GrammarChoiceTerm:
		options := make([]ast.GrammarTerm, 0, len(n.Options))
		helpers := make([]ast.GrammarProductionDecl, 0)
		for _, option := range n.Options {
			rewritten, extra := desugarNamedPrecedenceTerm(grammarName, production, option, counter)
			options = append(options, rewritten)
			helpers = append(helpers, extra...)
		}
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: options}, helpers
	case *ast.GrammarOptionalTerm:
		rewritten, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Term, counter)
		return &ast.GrammarOptionalTerm{Position: n.Position, Term: rewritten}, helpers
	case *ast.GrammarRequiredTerm:
		rewritten, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Term, counter)
		return &ast.GrammarRequiredTerm{Position: n.Position, Term: rewritten, Message: n.Message}, helpers
	case *ast.GrammarDelimitedTerm:
		open, openHelpers := desugarNamedPrecedenceTerm(grammarName, production, n.Open, counter)
		body, bodyHelpers := desugarNamedPrecedenceTerm(grammarName, production, n.Body, counter)
		closeTerm, closeHelpers := desugarNamedPrecedenceTerm(grammarName, production, n.Close, counter)
		helperCount := len(openHelpers) + len(bodyHelpers) + len(closeHelpers)
		helpers := make([]ast.GrammarProductionDecl, 0, helperCount)
		helpers = append(helpers, openHelpers...)
		helpers = append(helpers, bodyHelpers...)
		helpers = append(helpers, closeHelpers...)
		return &ast.GrammarDelimitedTerm{Position: n.Position, Open: open, Body: body, Close: closeTerm, Message: n.Message}, helpers
	case *ast.GrammarSeqTerm:
		terms := make([]ast.GrammarTerm, 0, len(n.Terms))
		helpers := make([]ast.GrammarProductionDecl, 0)
		for _, term := range n.Terms {
			rewritten, extra := desugarNamedPrecedenceTerm(grammarName, production, term, counter)
			terms = append(terms, rewritten)
			helpers = append(helpers, extra...)
		}
		return &ast.GrammarSeqTerm{Position: n.Position, Terms: terms}, helpers
	case *ast.GrammarLookaheadTerm:
		rewritten, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Term, counter)
		return &ast.GrammarLookaheadTerm{Position: n.Position, Term: rewritten}, helpers
	case *ast.GrammarSingletonTerm:
		return &ast.GrammarSingletonTerm{Position: n.Position, Type: n.Type, Value: n.Value}, nil
	case *ast.GrammarEmptyTerm:
		return &ast.GrammarEmptyTerm{Position: n.Position, Type: n.Type}, nil
	case *ast.GrammarConcatTerm:
		terms := make([]ast.GrammarTerm, 0, len(n.Terms))
		helpers := make([]ast.GrammarProductionDecl, 0)
		for _, term := range n.Terms {
			rewritten, extra := desugarNamedPrecedenceTerm(grammarName, production, term, counter)
			terms = append(terms, rewritten)
			helpers = append(helpers, extra...)
		}
		return &ast.GrammarConcatTerm{Position: n.Position, Terms: terms}, helpers
	case *ast.GrammarListTerm:
		elem, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Elem, counter)
		var separator ast.GrammarTerm
		if n.Separator != nil {
			var extra []ast.GrammarProductionDecl
			separator, extra = desugarNamedPrecedenceTerm(grammarName, production, n.Separator, counter)
			helpers = append(helpers, extra...)
		}
		until := make([]ast.GrammarTerm, 0, len(n.Until))
		for _, stop := range n.Until {
			rewritten, extra := desugarNamedPrecedenceTerm(grammarName, production, stop, counter)
			until = append(until, rewritten)
			helpers = append(helpers, extra...)
		}
		return &ast.GrammarListTerm{Position: n.Position, Elem: elem, Separator: separator, Until: until}, helpers
	case *ast.GrammarRepeatTerm:
		elem, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Elem, counter)
		until := make([]ast.GrammarTerm, 0, len(n.Until))
		for _, stop := range n.Until {
			rewritten, extra := desugarNamedPrecedenceTerm(grammarName, production, stop, counter)
			until = append(until, rewritten)
			helpers = append(helpers, extra...)
		}
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: elem, Until: until, MinOne: n.MinOne}, helpers
	case *ast.GrammarFlatRepeatTerm:
		elem, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Elem, counter)
		until := make([]ast.GrammarTerm, 0, len(n.Until))
		for _, stop := range n.Until {
			rewritten, extra := desugarNamedPrecedenceTerm(grammarName, production, stop, counter)
			until = append(until, rewritten)
			helpers = append(helpers, extra...)
		}
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: elem, Until: until}, helpers
	case *ast.GrammarWhileTerm:
		elem, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Elem, counter)
		until := make([]ast.GrammarTerm, 0, len(n.Until))
		for _, stop := range n.Until {
			rewritten, extra := desugarNamedPrecedenceTerm(grammarName, production, stop, counter)
			until = append(until, rewritten)
			helpers = append(helpers, extra...)
		}
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: elem, Until: until}, helpers
	case *ast.GrammarSeparatedTerm:
		elem, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Elem, counter)
		separator, extra := desugarNamedPrecedenceTerm(grammarName, production, n.Separator, counter)
		helpers = append(helpers, extra...)
		until := make([]ast.GrammarTerm, 0, len(n.Until))
		for _, stop := range n.Until {
			rewritten, more := desugarNamedPrecedenceTerm(grammarName, production, stop, counter)
			until = append(until, rewritten)
			helpers = append(helpers, more...)
		}
		return &ast.GrammarSeparatedTerm{Position: n.Position, Elem: elem, Separator: separator, Until: until}, helpers
	case *ast.GrammarPrecedenceTerm:
		if len(n.Levels) != 0 {
			return desugarNamedPrecedenceBlock(grammarName, production, n, counter)
		}
		if n.Assoc != "" {
			rewritten, helpers := desugarAssociativeInlinePrecedenceTerm(grammarName, production, n, counter)
			return &ast.GrammarBindTerm{Position: n.Position, Name: n.LeftName, Term: rewritten}, helpers
		}
		seed, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Seed, counter)
		arms := make([]ast.GrammarPrecedenceArm, 0, len(n.Arms))
		for _, arm := range n.Arms {
			rewrittenArm, extra := desugarNamedPrecedenceArm(grammarName, production, arm, counter)
			arms = append(arms, rewrittenArm)
			helpers = append(helpers, extra...)
		}
		return &ast.GrammarPrecedenceTerm{Position: n.Position, Assoc: n.Assoc, LeftName: n.LeftName, Seed: seed, Arms: arms}, helpers
	default:
		return term, nil
	}
}
func desugarNamedPrecedenceArm(grammarName string, production ast.GrammarProductionDecl, arm ast.GrammarPrecedenceArm, counter *int) (ast.GrammarPrecedenceArm, []ast.GrammarProductionDecl) {
	op, helpers := desugarNamedPrecedenceTerm(grammarName, production, arm.Op, counter)
	bindings := make([]*ast.GrammarBindTerm, 0, len(arm.Bindings))
	for _, binding := range arm.Bindings {
		rewritten, extra := desugarNamedPrecedenceTerm(grammarName, production, binding.Term, counter)
		bindings = append(bindings, &ast.GrammarBindTerm{Position: binding.Position, Name: binding.Name, Term: rewritten})
		helpers = append(helpers, extra...)
	}
	return ast.GrammarPrecedenceArm{Position: arm.Position, OpName: arm.OpName, Op: op, Block: arm.Block, Bindings: bindings, Value: arm.Value}, helpers
}
func desugarNamedPrecedenceBlock(grammarName string, production ast.GrammarProductionDecl, term *ast.GrammarPrecedenceTerm, counter *int) (ast.GrammarTerm, []ast.GrammarProductionDecl) {
	if term == nil {
		return nil, nil
	}
	*counter++
	blockIndex := *counter
	helperNames := make(map[string]string, len(term.Levels))
	for _, level := range term.Levels {
		helperNames[level.Name] = grammarPrecedenceHelperProductionName(grammarName, production.Name, blockIndex, level.Name)
	}
	paramArgs := grammarProductionParamArgs(production.Params)
	helpers := make([]ast.GrammarProductionDecl, 0, len(term.Levels))
	for _, level := range term.Levels {
		seed, extra := desugarNamedPrecedenceTerm(grammarName, production, level.Seed, counter)
		helpers = append(helpers, extra...)
		arms := make([]ast.GrammarPrecedenceArm, 0, len(level.Arms))
		for _, arm := range level.Arms {
			rewrittenArm, armHelpers := desugarNamedPrecedenceArm(grammarName, production, arm, counter)
			arms = append(arms, rewrittenArm)
			helpers = append(helpers, armHelpers...)
		}
		seed = rewriteNamedPrecedenceHelperCalls(seed, helperNames, paramArgs)
		for i := range arms {
			arms[i] = rewriteNamedPrecedenceArmCalls(arms[i], helperNames, paramArgs)
		}
		helperName := helperNames[level.Name]
		arms = applyNamedPrecedenceAssociativity(level, helperName, paramArgs, seed, arms)
		helpers = append(helpers, buildNamedPrecedenceHelperProduction(production, helperName, level, seed, arms))
	}
	topName := helperNames[term.Result]
	return &ast.GrammarCallTerm{Position: term.Position, Name: topName, Explicit: true, Args: grammarProductionParamArgs(production.Params)}, helpers
}
func desugarAssociativeInlinePrecedenceTerm(grammarName string, production ast.GrammarProductionDecl, term *ast.GrammarPrecedenceTerm, counter *int) (ast.GrammarTerm, []ast.GrammarProductionDecl) {
	if term == nil {
		return nil, nil
	}
	*counter++
	blockIndex := *counter
	helperName := grammarPrecedenceHelperProductionName(grammarName, production.Name, blockIndex, "inline")
	seed, helpers := desugarNamedPrecedenceTerm(grammarName, production, term.Seed, counter)
	arms := make([]ast.GrammarPrecedenceArm, 0, len(term.Arms))
	for _, arm := range term.Arms {
		rewrittenArm, armHelpers := desugarNamedPrecedenceArm(grammarName, production, arm, counter)
		arms = append(arms, rewrittenArm)
		helpers = append(helpers, armHelpers...)
	}
	paramArgs := grammarProductionParamArgs(production.Params)
	level := ast.GrammarPrecedenceLevel{Position: term.Position, Assoc: term.Assoc, Name: "inline", LeftName: term.LeftName, Seed: seed, Arms: arms}
	level.Arms = applyNamedPrecedenceAssociativity(level, helperName, paramArgs, seed, level.Arms)
	helpers = append(helpers, buildNamedPrecedenceHelperProduction(production, helperName, level, seed, level.Arms))
	return &ast.GrammarCallTerm{Position: term.Position, Name: helperName, Explicit: true, Args: paramArgs}, helpers
}
