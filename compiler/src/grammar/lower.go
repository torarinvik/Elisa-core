package grammar

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"strings"
)

type lowerContext struct {
	tokenReceiver string
}

type resolvedGrammarProduction struct {
	GrammarName string
	Production  ast.GrammarProductionDecl
	TryName     string
}

type grammarStateReceiverInfo struct {
	cursorReceiver string
	cursorField    string
	tokenReceiver  string
}

type statefulLowerContext struct {
	grammarName    string
	cursorReceiver string
	cursorField    string
	tokenReceiver  string
	tokenType      ast.TypeExpr
	allocName      string
	allocExpr      ast.Expr
	committedName  string
	channels       []ast.GrammarChannelDecl
	production     ast.GrammarProductionDecl
	productionMap  map[string]resolvedGrammarProduction
	tempCounter    int
}

func LowerFile(file *ast.File) *ast.File {
	if file == nil {
		return nil
	}
	return &ast.File{Filename: file.Filename, Decls: lowerDeclList(file.Decls)}
}

func lowerDeclList(decls []ast.Decl) []ast.Decl {
	return lowerDeclListInScope(decls, grammarDeclScope(decls))
}

func lowerDeclListInScope(decls []ast.Decl, grammarScope map[string]*ast.GrammarDecl) []ast.Decl {
	if len(decls) == 0 {
		return nil
	}
	lowered := make([]ast.Decl, 0, len(decls))
	loweredGrammarNames := make(map[string]bool)
	for _, decl := range decls {
		switch n := decl.(type) {
		case *ast.GrammarDecl:
			lowered = append(lowered, n)
			if loweredGrammarNames[n.Name] {
				continue
			}
			loweredGrammarNames[n.Name] = true
			merged := grammarScope[n.Name]
			if merged == nil {
				merged = n
			}
			lowered = append(lowered, lowerGrammarDecls(merged, grammarScope)...)
		case *ast.NamespaceDecl:
			cloned := &ast.NamespaceDecl{Position: n.Position, Name: n.Name, Decls: lowerDeclListInScope(n.Decls, grammarDeclScope(n.Decls))}
			lowered = append(lowered, cloned)
		default:
			lowered = append(lowered, decl)
		}
	}
	return lowered
}

func grammarDeclScope(decls []ast.Decl) map[string]*ast.GrammarDecl {
	if len(decls) == 0 {
		return nil
	}
	scope := make(map[string]*ast.GrammarDecl)
	for _, decl := range decls {
		grammarDecl, ok := decl.(*ast.GrammarDecl)
		if !ok || grammarDecl == nil || grammarDecl.Name == "" {
			continue
		}
		scope[grammarDecl.Name] = mergeGrammarDecls(scope[grammarDecl.Name], grammarDecl)
	}
	return scope
}

func cloneGrammarDecl(decl *ast.GrammarDecl) *ast.GrammarDecl {
	if decl == nil {
		return nil
	}
	cloned := *decl
	cloned.TypeParams = append([]string(nil), decl.TypeParams...)
	cloned.RefStorageParams = append([]string(nil), decl.RefStorageParams...)
	cloned.RefStateParams = append([]string(nil), decl.RefStateParams...)
	cloned.RegionParams = append([]string(nil), decl.RegionParams...)
	cloned.PermissionParams = append([]string(nil), decl.PermissionParams...)
	cloned.GenericParams = append([]ast.GenericParam(nil), decl.GenericParams...)
	cloned.Uses = append([]ast.TypeExpr(nil), decl.Uses...)
	cloned.TokenAliases = append([]ast.GrammarTokenAliasDecl(nil), decl.TokenAliases...)
	cloned.Channels = append([]ast.GrammarChannelDecl(nil), decl.Channels...)
	cloned.Productions = append([]ast.GrammarProductionDecl(nil), decl.Productions...)
	return &cloned
}

func mergeGrammarDecls(base *ast.GrammarDecl, extra *ast.GrammarDecl) *ast.GrammarDecl {
	if base == nil {
		return cloneGrammarDecl(extra)
	}
	if extra == nil {
		return cloneGrammarDecl(base)
	}
	merged := cloneGrammarDecl(base)
	if merged.Name == "" {
		merged.Name = extra.Name
	}
	if len(merged.TypeParams) == 0 {
		merged.TypeParams = append([]string(nil), extra.TypeParams...)
	}
	if len(merged.RefStorageParams) == 0 {
		merged.RefStorageParams = append([]string(nil), extra.RefStorageParams...)
	}
	if len(merged.RefStateParams) == 0 {
		merged.RefStateParams = append([]string(nil), extra.RefStateParams...)
	}
	if len(merged.RegionParams) == 0 {
		merged.RegionParams = append([]string(nil), extra.RegionParams...)
	}
	if len(merged.PermissionParams) == 0 {
		merged.PermissionParams = append([]string(nil), extra.PermissionParams...)
	}
	if len(merged.GenericParams) == 0 {
		merged.GenericParams = append([]ast.GenericParam(nil), extra.GenericParams...)
	}
	if merged.OverType == nil {
		merged.OverType = extra.OverType
	}
	if merged.UsingType == nil {
		merged.UsingType = extra.UsingType
	}
	merged.Uses = append(merged.Uses, extra.Uses...)
	if merged.ErrorType == nil {
		merged.ErrorType = extra.ErrorType
	}
	if merged.CursorExpr == nil {
		merged.CursorExpr = extra.CursorExpr
	}
	if merged.AllocExpr == nil {
		merged.AllocExpr = extra.AllocExpr
	}
	merged.TokenAliases = append(merged.TokenAliases, extra.TokenAliases...)
	merged.Channels = append(merged.Channels, extra.Channels...)
	merged.Productions = append(merged.Productions, extra.Productions...)
	merged.Extend = false
	return merged
}

func lowerGrammarDecls(decl *ast.GrammarDecl, grammarScope map[string]*ast.GrammarDecl) []ast.Decl {
	if decl == nil {
		return nil
	}
	normalizedDecl := normalizeGrammarDeclForLowering(decl)
	if !grammarDeclIsStateful(normalizedDecl) {
		public := LowerDecl(normalizedDecl)
		out := make([]ast.Decl, 0, len(public))
		for _, fn := range public {
			out = append(out, fn)
		}
		return out
	}
	rewrittenProductions := make([]ast.GrammarProductionDecl, 0, len(normalizedDecl.Productions))
	helperProductions := make([]ast.GrammarProductionDecl, 0)
	for _, production := range normalizedDecl.Productions {
		rewritten, helpers := desugarNamedPrecedenceProduction(normalizedDecl.Name, production)
		rewrittenProductions = append(rewrittenProductions, rewritten)
		helperProductions = append(helperProductions, helpers...)
	}
	productionMap := reachableGrammarProductionMap(normalizedDecl, grammarScope, rewrittenProductions, helperProductions)
	out := make([]ast.Decl, 0, len(rewrittenProductions)*3+len(helperProductions))
	for _, production := range rewrittenProductions {
		receiver := grammarReceiverInfoForProduction(normalizedDecl, production)
		ctx := &statefulLowerContext{
			grammarName:    normalizedDecl.Name,
			cursorReceiver: receiver.cursorReceiver,
			cursorField:    receiver.cursorField,
			tokenReceiver:  receiver.tokenReceiver,
			tokenType:      grammarDeclTokenType(normalizedDecl, production.Position),
			allocName:      grammarAllocNameForProduction(normalizedDecl, production),
			allocExpr:      grammarAllocExprForProduction(normalizedDecl, production, receiver, production.Position),
			channels:       append([]ast.GrammarChannelDecl(nil), normalizedDecl.Channels...),
			production:     production,
			productionMap:  productionMap,
		}
		out = append(out, lowerStatefulPublicProduction(normalizedDecl, ctx))
		out = append(out, lowerStatefulPublicTryProduction(normalizedDecl, ctx))
		out = append(out, lowerStatefulTryProduction(normalizedDecl, ctx))
	}
	for _, production := range helperProductions {
		receiver := grammarReceiverInfoForProduction(normalizedDecl, production)
		ctx := &statefulLowerContext{
			grammarName:    normalizedDecl.Name,
			cursorReceiver: receiver.cursorReceiver,
			cursorField:    receiver.cursorField,
			tokenReceiver:  receiver.tokenReceiver,
			tokenType:      grammarDeclTokenType(normalizedDecl, production.Position),
			allocName:      grammarAllocNameForProduction(normalizedDecl, production),
			allocExpr:      grammarAllocExprForProduction(normalizedDecl, production, receiver, production.Position),
			channels:       append([]ast.GrammarChannelDecl(nil), normalizedDecl.Channels...),
			production:     production,
			productionMap:  productionMap,
		}
		out = append(out, lowerStatefulTryProduction(normalizedDecl, ctx))
	}
	return out
}

func reachableGrammarProductionMap(grammarDecl *ast.GrammarDecl, grammarScope map[string]*ast.GrammarDecl, localProductions []ast.GrammarProductionDecl, helperProductions []ast.GrammarProductionDecl) map[string]resolvedGrammarProduction {
	resolved := make(map[string]resolvedGrammarProduction, len(localProductions)+len(helperProductions))
	for _, production := range localProductions {
		resolved[production.Name] = resolvedGrammarProduction{GrammarName: grammarDecl.Name, Production: production, TryName: grammarTryFuncName(grammarDecl.Name, production.Name)}
	}
	for _, production := range helperProductions {
		resolved[production.Name] = resolvedGrammarProduction{GrammarName: grammarDecl.Name, Production: production, TryName: grammarTryFuncName(grammarDecl.Name, production.Name)}
	}
	seen := map[string]bool{grammarDecl.Name: true}
	appendUsedGrammarProductions(resolved, grammarDecl, grammarScope, seen)
	return resolved
}

func appendUsedGrammarProductions(resolved map[string]resolvedGrammarProduction, grammarDecl *ast.GrammarDecl, grammarScope map[string]*ast.GrammarDecl, seen map[string]bool) {
	if grammarDecl == nil || len(grammarDecl.Uses) == 0 || grammarScope == nil {
		return
	}
	for _, used := range grammarDecl.Uses {
		usedName := grammarUseName(used)
		if usedName == "" || seen[usedName] {
			continue
		}
		usedDecl, ok := grammarScope[usedName]
		if !ok || usedDecl == nil {
			continue
		}
		seen[usedName] = true
		normalizedUsed := normalizeGrammarDeclForLowering(usedDecl)
		for _, production := range normalizedUsed.Productions {
			if _, exists := resolved[production.Name]; exists {
				continue
			}
			resolved[production.Name] = resolvedGrammarProduction{GrammarName: normalizedUsed.Name, Production: production, TryName: grammarTryFuncName(normalizedUsed.Name, production.Name)}
		}
		appendUsedGrammarProductions(resolved, normalizedUsed, grammarScope, seen)
	}
}

func grammarUseName(typ ast.TypeExpr) string {
	switch n := typ.(type) {
	case *ast.NamedType:
		return n.Name
	case *ast.GenericType:
		return n.Name
	default:
		return ""
	}
}

func normalizeGrammarDeclForLowering(decl *ast.GrammarDecl) *ast.GrammarDecl {
	if decl == nil {
		return nil
	}
	normalized := *decl
	normalized.TokenAliases = append([]ast.GrammarTokenAliasDecl(nil), decl.TokenAliases...)
	aliasByLiteral := grammarTokenAliasLiteralMap(decl.TokenAliases)
	normalized.Productions = make([]ast.GrammarProductionDecl, 0, len(decl.Productions))
	for _, production := range decl.Productions {
		rewritten := rewriteGrammarProductionTokenAliases(production, aliasByLiteral)
		normalized.Productions = append(normalized.Productions, rewritten)
	}
	normalized.Productions = expandAugmentedGrammarProductions(normalized.Productions)
	finalProductions := make([]ast.GrammarProductionDecl, 0, len(normalized.Productions))
	for _, production := range normalized.Productions {
		finalProductions = append(finalProductions, normalizeGrammarProductionForLowering(&normalized, production))
	}
	normalized.Productions = finalProductions
	return &normalized
}

func grammarTokenAliasLiteralMap(aliases []ast.GrammarTokenAliasDecl) map[string]string {
	if len(aliases) == 0 {
		return nil
	}
	byLiteral := make(map[string]string, len(aliases))
	for _, alias := range aliases {
		if alias.HasLiteral && alias.Literal != "" {
			byLiteral[alias.Literal] = alias.Kind
		}
	}
	if len(byLiteral) == 0 {
		return nil
	}
	return byLiteral
}

func rewriteGrammarProductionTokenAliases(production ast.GrammarProductionDecl, aliases map[string]string) ast.GrammarProductionDecl {
	if len(aliases) == 0 {
		return production
	}
	production.RecoverUntil = rewriteGrammarTermListTokenAliases(production.RecoverUntil, aliases)
	terms := make([]ast.GrammarTerm, 0, len(production.Terms))
	for _, term := range production.Terms {
		terms = append(terms, rewriteGrammarTermTokenAliases(term, aliases))
	}
	production.Terms = terms
	return production
}

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
		rewritten = append(rewritten, ast.GrammarPostfixArm{Position: arm.Position, OpName: arm.OpName, Op: rewriteGrammarTermTokenAliases(arm.Op, aliases), Bindings: rewriteGrammarBindingsTokenAliases(arm.Bindings, aliases), Value: arm.Value})
	}
	return rewritten
}

func rewriteGrammarPrecedenceArmsTokenAliases(arms []ast.GrammarPrecedenceArm, aliases map[string]string) []ast.GrammarPrecedenceArm {
	if len(arms) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarPrecedenceArm, 0, len(arms))
	for _, arm := range arms {
		rewritten = append(rewritten, ast.GrammarPrecedenceArm{Position: arm.Position, OpName: arm.OpName, Op: rewriteGrammarTermTokenAliases(arm.Op, aliases), Bindings: rewriteGrammarBindingsTokenAliases(arm.Bindings, aliases), Value: arm.Value})
	}
	return rewritten
}

func rewriteGrammarPrecedenceLevelsTokenAliases(levels []ast.GrammarPrecedenceLevel, aliases map[string]string) []ast.GrammarPrecedenceLevel {
	if len(levels) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarPrecedenceLevel, 0, len(levels))
	for _, level := range levels {
		rewritten = append(rewritten, ast.GrammarPrecedenceLevel{Position: level.Position, Name: level.Name, LeftName: level.LeftName, Seed: rewriteGrammarTermTokenAliases(level.Seed, aliases), Arms: rewriteGrammarPrecedenceArmsTokenAliases(level.Arms, aliases)})
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
	case *ast.GrammarChoiceTerm:
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: rewriteGrammarTermListTokenAliases(n.Options, aliases)}
	case *ast.GrammarOptionalTerm:
		return &ast.GrammarOptionalTerm{Position: n.Position, Term: rewriteGrammarTermTokenAliases(n.Term, aliases)}
	case *ast.GrammarWhenTerm:
		return &ast.GrammarWhenTerm{Position: n.Position, Cond: n.Cond, Then: rewriteGrammarTermTokenAliases(n.Then, aliases), Else: rewriteGrammarTermTokenAliases(n.Else, aliases)}
	case *ast.GrammarRecoverTerm:
		return &ast.GrammarRecoverTerm{Position: n.Position, Term: rewriteGrammarTermTokenAliases(n.Term, aliases), RecoverMsg: n.RecoverMsg, RecoverUntil: rewriteGrammarTermListTokenAliases(n.RecoverUntil, aliases), RecoverValue: n.RecoverValue}
	case *ast.GrammarRequiredTerm:
		return &ast.GrammarRequiredTerm{Position: n.Position, Term: rewriteGrammarTermTokenAliases(n.Term, aliases), Message: n.Message}
	case *ast.GrammarDelimitedTerm:
		return &ast.GrammarDelimitedTerm{Position: n.Position, Open: rewriteGrammarTermTokenAliases(n.Open, aliases), Body: rewriteGrammarTermTokenAliases(n.Body, aliases), Close: rewriteGrammarTermTokenAliases(n.Close, aliases), Message: n.Message}
	case *ast.GrammarSeqTerm:
		return &ast.GrammarSeqTerm{Position: n.Position, Terms: rewriteGrammarTermListTokenAliases(n.Terms, aliases)}
	case *ast.GrammarLookaheadTerm:
		return &ast.GrammarLookaheadTerm{Position: n.Position, Term: rewriteGrammarTermTokenAliases(n.Term, aliases)}
	case *ast.GrammarListTerm:
		return &ast.GrammarListTerm{Position: n.Position, Elem: rewriteGrammarTermTokenAliases(n.Elem, aliases), Separator: rewriteGrammarTermTokenAliases(n.Separator, aliases), Until: rewriteGrammarTermListTokenAliases(n.Until, aliases)}
	case *ast.GrammarRepeatTerm:
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: rewriteGrammarTermTokenAliases(n.Elem, aliases), Until: rewriteGrammarTermListTokenAliases(n.Until, aliases)}
	case *ast.GrammarFlatRepeatTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: rewriteGrammarTermTokenAliases(n.Elem, aliases), Until: rewriteGrammarTermListTokenAliases(n.Until, aliases)}
	case *ast.GrammarSeparatedTerm:
		return &ast.GrammarSeparatedTerm{Position: n.Position, Elem: rewriteGrammarTermTokenAliases(n.Elem, aliases), Separator: rewriteGrammarTermTokenAliases(n.Separator, aliases), Until: rewriteGrammarTermListTokenAliases(n.Until, aliases)}
	case *ast.GrammarSuffixTerm:
		return &ast.GrammarSuffixTerm{Position: n.Position, LeftName: n.LeftName, Seed: rewriteGrammarTermTokenAliases(n.Seed, aliases), Arms: rewriteGrammarPostfixArmsTokenAliases(n.Arms, aliases)}
	case *ast.GrammarPostfixTerm:
		return &ast.GrammarPostfixTerm{Position: n.Position, LeftName: n.LeftName, Seed: rewriteGrammarTermTokenAliases(n.Seed, aliases), Arms: rewriteGrammarPostfixArmsTokenAliases(n.Arms, aliases)}
	case *ast.GrammarPrecedenceTerm:
		return &ast.GrammarPrecedenceTerm{Position: n.Position, Result: n.Result, Levels: rewriteGrammarPrecedenceLevelsTokenAliases(n.Levels, aliases), LeftName: n.LeftName, Seed: rewriteGrammarTermTokenAliases(n.Seed, aliases), Arms: rewriteGrammarPrecedenceArmsTokenAliases(n.Arms, aliases)}
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
	helpers = append(helpers, ast.GrammarProductionDecl{Position: base.Position, Name: helperBaseName, HasParamList: base.HasParamList || len(base.Params) != 0, Params: append([]ast.ParamDecl(nil), base.Params...), ReturnType: base.ReturnType, Terms: append([]ast.GrammarTerm(nil), base.Terms...)})
	options := []ast.GrammarTerm{grammarAugmentHelperCall(base.Position, helperBaseName, base.Params)}
	for index, appendProduction := range appends {
		helperName := grammarAugmentHelperProductionName(base.Name, "append", index+1)
		helpers = append(helpers, ast.GrammarProductionDecl{Position: appendProduction.Position, Name: helperName, HasParamList: base.HasParamList || len(base.Params) != 0, Params: append([]ast.ParamDecl(nil), base.Params...), ReturnType: base.ReturnType, Terms: append([]ast.GrammarTerm(nil), appendProduction.Terms...)})
		options = append(options, grammarAugmentHelperCall(appendProduction.Position, helperName, base.Params))
	}
	merged := base
	if base.ReturnType != nil {
		resultName := grammarAugmentResultName(base.Name)
		merged.Terms = []ast.GrammarTerm{
			&ast.GrammarBindTerm{Position: base.Position, Name: resultName, Term: &ast.GrammarChoiceTerm{Position: base.Position, Options: options}},
			&ast.GrammarReturnTerm{Position: base.Position, Value: &ast.Ident{Position: base.Position, Name: resultName}},
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
		rewritten, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Term, counter)
		return &ast.GrammarBindTerm{Position: n.Position, Name: n.Name, Term: rewritten}, helpers
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
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: elem, Until: until}, helpers
	case *ast.GrammarFlatRepeatTerm:
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
		seed, helpers := desugarNamedPrecedenceTerm(grammarName, production, n.Seed, counter)
		arms := make([]ast.GrammarPrecedenceArm, 0, len(n.Arms))
		for _, arm := range n.Arms {
			rewrittenArm, extra := desugarNamedPrecedenceArm(grammarName, production, arm, counter)
			arms = append(arms, rewrittenArm)
			helpers = append(helpers, extra...)
		}
		return &ast.GrammarPrecedenceTerm{Position: n.Position, LeftName: n.LeftName, Seed: seed, Arms: arms}, helpers
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
	return ast.GrammarPrecedenceArm{Position: arm.Position, OpName: arm.OpName, Op: op, Bindings: bindings, Value: arm.Value}, helpers
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
		helpers = append(helpers, buildNamedPrecedenceHelperProduction(production, helperName, level, seed, arms))
	}
	topName := helperNames[term.Result]
	return &ast.GrammarCallTerm{Position: term.Position, Name: topName, Explicit: true, Args: grammarProductionParamArgs(production.Params)}, helpers
}

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
		term = &ast.GrammarPrecedenceTerm{Position: level.Position, LeftName: level.LeftName, Seed: seed, Arms: arms}
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
			&ast.GrammarReturnTerm{Position: level.Position, Value: &ast.Ident{Position: level.Position, Name: resultName}},
		},
	}
}

func rewriteNamedPrecedenceArmCalls(arm ast.GrammarPrecedenceArm, helperNames map[string]string, paramArgs []ast.Expr) ast.GrammarPrecedenceArm {
	op := rewriteNamedPrecedenceHelperCalls(arm.Op, helperNames, paramArgs)
	bindings := make([]*ast.GrammarBindTerm, 0, len(arm.Bindings))
	for _, binding := range arm.Bindings {
		bindings = append(bindings, &ast.GrammarBindTerm{Position: binding.Position, Name: binding.Name, Term: rewriteNamedPrecedenceHelperCalls(binding.Term, helperNames, paramArgs)})
	}
	return ast.GrammarPrecedenceArm{Position: arm.Position, OpName: arm.OpName, Op: op, Bindings: bindings, Value: arm.Value}
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
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: rewriteNamedPrecedenceHelperCalls(n.Elem, helperNames, paramArgs), Until: until}
	case *ast.GrammarFlatRepeatTerm:
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
		return &ast.GrammarPrecedenceTerm{Position: n.Position, LeftName: n.LeftName, Seed: rewriteNamedPrecedenceHelperCalls(n.Seed, helperNames, paramArgs), Arms: arms}
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
		RefStorageParams: append([]string(nil), grammarDecl.RefStorageParams...),
		RefStateParams:   append([]string(nil), grammarDecl.RefStateParams...),
		RegionParams:     append([]string(nil), grammarDecl.RegionParams...),
		PermissionParams: append([]string(nil), grammarDecl.PermissionParams...),
		GenericParams:    append([]ast.GenericParam(nil), grammarDecl.GenericParams...),
		Params:           append([]ast.ParamDecl(nil), ctx.production.Params...),
		ReturnType:       ctx.production.ReturnType,
		Body:             grammarCanBlock(ctx.production.Position, body),
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
		RefStorageParams: append([]string(nil), grammarDecl.RefStorageParams...),
		RefStateParams:   append([]string(nil), grammarDecl.RefStateParams...),
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
				Func:     &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: ctx.tokenReceiver}, Field: "record_parse_error"},
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
					Left:     &ast.FieldExpr{Position: pos, Object: currentTokenExpr(ctx.tokenReceiver, pos), Field: "kind"},
					Right:    &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: "TokenKind"}, Field: "EOF"},
				},
			},
			Body: []ast.Stmt{
				&ast.ExprStmt{Position: pos, Expr: &ast.CallExpr{Position: pos, Func: &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: ctx.tokenReceiver}, Field: "advance_token"}}},
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
	body = append(body, ctx.successTupleReturnStmts(ctx.production.Position, zeroedCastExpr(ctx.production.Position, grammarResolvedValueTypeExpr(ctx.production.Position, ctx.production.ReturnType)))...)
	return &ast.FuncDecl{
		Position:         ctx.production.Position,
		Name:             grammarTryFuncName(ctx.grammarName, ctx.production.Name),
		TypeParams:       append([]string(nil), grammarDecl.TypeParams...),
		RefStorageParams: append([]string(nil), grammarDecl.RefStorageParams...),
		RefStateParams:   append([]string(nil), grammarDecl.RefStateParams...),
		RegionParams:     append([]string(nil), grammarDecl.RegionParams...),
		PermissionParams: append([]string(nil), grammarDecl.PermissionParams...),
		GenericParams:    append([]ast.GenericParam(nil), grammarDecl.GenericParams...),
		Params:           append([]ast.ParamDecl(nil), ctx.production.Params...),
		ReturnType:       grammarInternalTryReturnTypeExpr(ctx.production.Position, ctx.production.ReturnType),
		Body:             grammarCanBlock(ctx.production.Position, body),
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

func grammarDefaultPermissions(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{
		{Position: pos, Name: "Abort", Member: "Panic"},
		{Position: pos, Name: "Memory", Member: "Allocate"},
	}
}

func builtinTypeExpr(pos lexer.Pos, name string) ast.TypeExpr {
	return &ast.NamedType{Position: pos, Name: name}
}

func listTypeExpr(pos lexer.Pos, elemType ast.TypeExpr) ast.TypeExpr {
	if elemType == nil {
		elemType = builtinTypeExpr(pos, "void")
	}
	return &ast.BuiltinTypeExpr{Position: pos, Name: "darray", TypeArgs: []ast.TypeExpr{elemType}}
}

func zeroedCastExpr(pos lexer.Pos, target ast.TypeExpr) ast.Expr {
	return &ast.CastExpr{Position: pos, Operand: &ast.ZeroedLit{Position: pos}, Target: target}
}

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
		startExpr := currentTokenExpr(ctx.tokenReceiver, ctx.production.Position)
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
		stmts = append(stmts, &ast.AssignStmt{Position: pos, Target: &ast.Ident{Position: pos, Name: "$end"}, Value: currentTokenExpr(ctx.tokenReceiver, pos)})
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

func (ctx *statefulLowerContext) successTupleReturnStmts(pos lexer.Pos, value ast.Expr) []ast.Stmt {
	stmts := ctx.lowerChannelFinalize(pos)
	return append(stmts, successTupleReturnStmt(pos, ctx.currentCommittedExpr(pos), value))
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

func currentTokenExpr(stateName string, pos lexer.Pos) ast.Expr {
	return &ast.CallExpr{
		Position: pos,
		Func: &ast.FieldExpr{
			Position: pos,
			Object:   &ast.Ident{Position: pos, Name: stateName},
			Field:    "current_token",
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

func grammarTokenMatchExpr(pos lexer.Pos, tokenExpr ast.Expr, value string) ast.Expr {
	return grammarTokenKindMatchExpr(pos, tokenExpr, &ast.CallExpr{
		Position: pos,
		Func:     &ast.Ident{Position: pos, Name: "token_kind_for_text"},
		Args:     []ast.Expr{&ast.StringLit{Position: pos, Value: value}},
	})
}

func grammarTokenKindMatchExpr(pos lexer.Pos, tokenExpr ast.Expr, kindExpr ast.Expr) ast.Expr {
	return &ast.BinaryExpr{
		Position: pos,
		Op:       lexer.TOKEN_EQEQ,
		Left:     &ast.FieldExpr{Position: pos, Object: tokenExpr, Field: "kind"},
		Right:    kindExpr,
	}
}

func grammarTokenKindMatcher(term *ast.GrammarCallTerm) (ast.Expr, bool) {
	if term == nil || term.Name != "token" || !term.Explicit || len(term.Args) != 1 {
		return nil, false
	}
	return term.Args[0], true
}

func grammarTokenKindExpr(pos lexer.Pos, kind string) ast.Expr {
	return &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: "TokenKind"}, Field: kind}
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
		return ctx.successTupleReturnStmts(n.Position, n.Value)
	case *ast.GrammarBindTerm:
		attempt := ctx.lowerAttempt(n.Term)
		result := append([]ast.Stmt{}, attempt.Stmts...)
		result = append(result, ctx.markAttemptCommittedStmts(n.Term.Pos(), attempt.Committed)...)
		if ctx.termCanFail(n.Term) {
			result = append(result, ctx.failureGuard(n.Term.Pos(), snapshotName, attempt.Matched)...)
		}
		result = append(result, &ast.VarDeclStmt{Position: n.Position, Name: n.Name, Value: attempt.Value})
		return result
	case *ast.GrammarAssignTerm:
		attempt := ctx.lowerAttempt(n.Term)
		result := append([]ast.Stmt{}, attempt.Stmts...)
		result = append(result, ctx.markAttemptCommittedStmts(n.Term.Pos(), attempt.Committed)...)
		if ctx.termCanFail(n.Term) {
			result = append(result, ctx.failureGuard(n.Term.Pos(), snapshotName, attempt.Matched)...)
		}
		result = append(result, &ast.AssignStmt{Position: n.Position, Target: &ast.Ident{Position: n.Position, Name: n.Name}, Value: attempt.Value})
		if flagName, ok := ctx.channelSetFlagName(n.Name); ok {
			result = append(result, &ast.AssignStmt{Position: n.Position, Target: &ast.Ident{Position: n.Position, Name: flagName}, Value: &ast.BoolLit{Position: n.Position, Value: true}})
		}
		return result
	default:
		attempt := ctx.lowerAttempt(term)
		result := append([]ast.Stmt{}, attempt.Stmts...)
		result = append(result, ctx.markAttemptCommittedStmts(term.Pos(), attempt.Committed)...)
		if ctx.termCanFail(term) {
			result = append(result, ctx.failureGuard(term.Pos(), snapshotName, attempt.Matched)...)
		}
		if call, ok := term.(*ast.GrammarCallTerm); ok && !ctx.termCanFail(term) && attempt.Value != nil && ctx.callTermReturnsValue(call) {
			result = append(result, &ast.ExprStmt{Position: term.Pos(), Expr: attempt.Value})
		}
		return result
	}
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

func (ctx *statefulLowerContext) termCanFail(term ast.GrammarTerm) bool {
	switch n := term.(type) {
	case *ast.GrammarTokenTerm:
		return true
	case *ast.GrammarTokenKindTerm:
		return true
	case *ast.GrammarCallTerm:
		if _, ok := grammarTokenKindMatcher(n); ok {
			return true
		}
		_, production, ok := ctx.resolveGrammarProductionInfo(n)
		if ok && production.RecoverMsg != nil && len(production.RecoverUntil) != 0 {
			return false
		}
		return ok
	case *ast.GrammarChoiceTerm:
		return true
	case *ast.GrammarWhenTerm:
		return ctx.termCanFail(n.Then) || ctx.termCanFail(n.Else)
	case *ast.GrammarRequiredTerm:
		return false
	case *ast.GrammarDelimitedTerm:
		return ctx.termCanFail(n.Open) || ctx.termCanFail(n.Body)
	case *ast.GrammarSeqTerm:
		for _, term := range n.Terms {
			if ctx.termCanFail(term) {
				return true
			}
		}
		return false
	case *ast.GrammarLookaheadTerm:
		return ctx.termCanFail(n.Term)
	case *ast.GrammarExprTerm:
		return false
	case *ast.GrammarGuardTerm:
		return true
	case *ast.GrammarAttemptTerm:
		return true
	case *ast.GrammarCutTerm:
		return false
	case *ast.GrammarOptionalTerm:
		return false
	case *ast.GrammarListTerm:
		return false
	case *ast.GrammarRepeatTerm:
		return false
	case *ast.GrammarFlatRepeatTerm:
		return false
	case *ast.GrammarSeparatedTerm:
		return false
	case *ast.GrammarSuffixTerm:
		return ctx.termCanFail(n.Seed)
	case *ast.GrammarRecoverTerm:
		return false
	case *ast.GrammarPostfixTerm:
		return ctx.termCanFail(n.Seed)
	case *ast.GrammarPrecedenceTerm:
		return ctx.termCanFail(n.Seed)
	case *ast.GrammarBindTerm:
		return ctx.termCanFail(n.Term)
	default:
		return false
	}
}

func (ctx *statefulLowerContext) lowerAttempt(term ast.GrammarTerm) loweredAttempt {
	switch n := term.(type) {
	case *ast.GrammarTokenTerm:
		valueName := ctx.fresh("token")
		matchedName := ctx.fresh("matched")
		valueIdent := &ast.Ident{Position: n.Position, Name: valueName}
		return loweredAttempt{
			Stmts: []ast.Stmt{
				&ast.VarDeclStmt{Position: n.Position, Name: valueName, Value: lowerTermExpr(lowerContext{tokenReceiver: ctx.tokenReceiver}, n)},
				&ast.VarDeclStmt{Position: n.Position, Name: matchedName, Value: grammarTokenMatchExpr(n.Position, valueIdent, n.Value)},
			},
			Matched:   &ast.Ident{Position: n.Position, Name: matchedName},
			Committed: &ast.BoolLit{Position: n.Position, Value: false},
			Value:     valueIdent,
		}
	case *ast.GrammarTokenKindTerm:
		valueName := ctx.fresh("token")
		matchedName := ctx.fresh("matched")
		valueIdent := &ast.Ident{Position: n.Position, Name: valueName}
		kindExpr := grammarTokenKindExpr(n.Position, n.Kind)
		return loweredAttempt{
			Stmts: []ast.Stmt{
				&ast.VarDeclStmt{Position: n.Position, Name: valueName, Value: lowerTermExpr(lowerContext{tokenReceiver: ctx.tokenReceiver}, n)},
				&ast.VarDeclStmt{Position: n.Position, Name: matchedName, Value: grammarTokenKindMatchExpr(n.Position, valueIdent, kindExpr)},
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
					&ast.VarDeclStmt{Position: n.Position, Name: valueName, Value: lowerTermExpr(lowerContext{tokenReceiver: ctx.tokenReceiver}, n)},
					&ast.VarDeclStmt{Position: n.Position, Name: matchedName, Value: grammarTokenKindMatchExpr(n.Position, valueIdent, kindExpr)},
				},
				Matched:   &ast.Ident{Position: n.Position, Name: matchedName},
				Committed: &ast.BoolLit{Position: n.Position, Value: false},
				Value:     valueIdent,
			}
		}
		if _, production, ok := ctx.resolveGrammarProductionInfo(n); ok && production.RecoverMsg != nil && len(production.RecoverUntil) != 0 {
			valueExpr := lowerTermExpr(lowerContext{tokenReceiver: ctx.tokenReceiver}, n)
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
		valueExpr := lowerTermExpr(lowerContext{tokenReceiver: ctx.tokenReceiver}, n)
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
		return loweredAttempt{
			Stmts:     []ast.Stmt{&ast.VarDeclStmt{Position: n.Position, Name: valueName, Value: n.Expr}},
			Matched:   &ast.BoolLit{Position: n.Position, Value: true},
			Committed: &ast.BoolLit{Position: n.Position, Value: false},
			Value:     &ast.Ident{Position: n.Position, Name: valueName},
		}
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
	thenAttempt := ctx.lowerAttempt(term.Then)
	elseAttempt := ctx.lowerAttempt(term.Else)
	termType := ctx.inferTermType(term)
	if termType == nil {
		termType = grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType)
	}
	condName := ctx.fresh("when_cond")
	matchedName := ctx.fresh("when_matched")
	committedName := ctx.fresh("when_committed")
	valueName := ctx.fresh("when_value")
	valueIdent := &ast.Ident{Position: term.Position, Name: valueName}
	thenValue := thenAttempt.Value
	if thenValue == nil {
		thenValue = zeroedCastExpr(term.Position, termType)
	}
	elseValue := elseAttempt.Value
	if elseValue == nil {
		elseValue = zeroedCastExpr(term.Position, termType)
	}
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
			&ast.VarDeclStmt{Position: term.Position, Name: condName, Value: term.Cond},
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
	termType := ctx.inferTermType(term.Body)
	if termType == nil {
		termType = grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType)
	}
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
	termType := ctx.inferTermType(term)
	if termType == nil {
		termType = grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType)
	}
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
	termType := ctx.inferTermType(term.Term)
	if termType == nil {
		termType = grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType)
	}
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
		thenBranch = append(thenBranch,
			&ast.AssignStmt{Position: pos, Target: valueTarget, Value: attempt.Value},
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
		termType := ctx.inferTermType(term.Term)
		if termType == nil {
			termType = grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType)
		}
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
				Func:     &ast.FieldExpr{Position: term.Position, Object: &ast.Ident{Position: term.Position, Name: ctx.tokenReceiver}, Field: "record_parse_error"},
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
	termType := ctx.inferTermType(term.Term)
	if termType == nil {
		termType = grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType)
	}
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
	leftType := ctx.inferTermType(term.Seed)
	if leftType == nil {
		leftType = grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType)
	}
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
	termType := ctx.inferTermType(term)
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
	stmts = append(stmts, ctx.lowerChoiceOptions(term.Options, snapshotName, matchedName, committedName, valueName)...)
	return loweredAttempt{Stmts: stmts, Matched: &ast.Ident{Position: term.Position, Name: matchedName}, Committed: &ast.Ident{Position: term.Position, Name: committedName}, Value: &ast.Ident{Position: term.Position, Name: valueName}}
}

func (ctx *statefulLowerContext) lowerFlatRepeatAttempt(term *ast.GrammarFlatRepeatTerm) loweredAttempt {
	resultType := ctx.inferTermType(term)
	if resultType == nil {
		resultType = grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType)
	}
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

func (ctx *statefulLowerContext) lowerChoiceOptions(options []ast.GrammarTerm, snapshotName string, matchedName string, committedName string, valueName string) []ast.Stmt {
	if len(options) == 0 {
		return []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, ctx.production.Position)}
	}
	attempt := ctx.lowerAttempt(options[0])
	thenBranch := append(markCommittedStmts(committedName, options[0].Pos(), attempt.Committed),
		&ast.AssignStmt{Position: options[0].Pos(), Target: &ast.Ident{Position: options[0].Pos(), Name: matchedName}, Value: &ast.BoolLit{Position: options[0].Pos(), Value: true}},
		&ast.AssignStmt{Position: options[0].Pos(), Target: &ast.Ident{Position: options[0].Pos(), Name: valueName}, Value: attempt.Value},
	)
	fallback := []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, options[0].Pos())}
	if len(options) > 1 {
		fallback = append(fallback, ctx.lowerChoiceOptions(options[1:], snapshotName, matchedName, committedName, valueName)...)
	}
	result := append([]ast.Stmt{}, attempt.Stmts...)
	result = append(result, &ast.IfStmt{Position: options[0].Pos(), Cond: attempt.Matched, Then: thenBranch, Else: []ast.Stmt{&ast.IfStmt{Position: options[0].Pos(), Cond: attempt.Committed, Then: []ast.Stmt{committedAssignTrueStmt(committedName, options[0].Pos())}, Else: fallback}}})
	return result
}

func (ctx *statefulLowerContext) lowerOptionalAttempt(term *ast.GrammarOptionalTerm) loweredAttempt {
	inner := ctx.lowerAttempt(term.Term)
	snapshotName := ctx.fresh("optional_cursor")
	termType := ctx.inferTermType(term.Term)
	matchedName := ctx.fresh("optional_matched")
	committedName := ctx.fresh("optional_committed")
	valueName := ctx.fresh("optional_value")
	stms := []ast.Stmt{
		&ast.VarDeclStmt{Position: term.Position, Name: snapshotName, Value: stateCursorExpr(ctx.cursorReceiver, ctx.cursorField, term.Position)},
		&ast.VarDeclStmt{Position: term.Position, Name: matchedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: true}},
		&ast.VarDeclStmt{Position: term.Position, Name: committedName, Mutable: true, Type: builtinTypeExpr(term.Position, "bool"), Value: &ast.BoolLit{Position: term.Position, Value: false}},
		&ast.VarDeclStmt{Position: term.Position, Name: valueName, Mutable: true, Type: termType, Value: zeroedCastExpr(term.Position, termType)},
	}
	stms = append(stms, inner.Stmts...)
	stms = append(stms, &ast.IfStmt{Position: term.Position, Cond: inner.Matched, Then: append(markCommittedStmts(committedName, term.Position, inner.Committed), &ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: valueName}, Value: inner.Value}), Else: []ast.Stmt{&ast.IfStmt{Position: term.Position, Cond: inner.Committed, Then: []ast.Stmt{&ast.AssignStmt{Position: term.Position, Target: &ast.Ident{Position: term.Position, Name: matchedName}, Value: &ast.BoolLit{Position: term.Position, Value: false}}, committedAssignTrueStmt(committedName, term.Position)}, Else: []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, term.Position)}}}})
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
	leftType := ctx.inferTermType(term.Seed)
	if leftType == nil {
		leftType = grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType)
	}
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
			}, ctx.lowerPrecedenceArms(term.Arms, snapshotName, stopName, matchedName, committedName, leftName)...),
		},
	)
	return loweredAttempt{Stmts: stmts, Matched: &ast.Ident{Position: term.Position, Name: matchedName}, Committed: &ast.Ident{Position: term.Position, Name: committedName}, Value: &ast.Ident{Position: term.Position, Name: leftName}}
}

func (ctx *statefulLowerContext) lowerPostfixAttempt(term *ast.GrammarPostfixTerm) loweredAttempt {
	seedAttempt := ctx.lowerAttempt(term.Seed)
	leftType := ctx.inferTermType(term.Seed)
	if leftType == nil {
		leftType = grammarResolvedValueTypeExpr(term.Position, ctx.production.ReturnType)
	}
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

func (ctx *statefulLowerContext) lowerPrecedenceArms(arms []ast.GrammarPrecedenceArm, snapshotName string, stopName string, matchedName string, committedName string, leftName string) []ast.Stmt {
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
		bindAttempt := ctx.lowerAttempt(binding.Term)
		thenBranch = append(thenBranch, bindAttempt.Stmts...)
		thenBranch = append(thenBranch, markCommittedStmts(committedName, binding.Position, bindAttempt.Committed)...)
		if ctx.termCanFail(binding.Term) {
			thenBranch = append(thenBranch, ctx.failureGuard(binding.Term.Pos(), snapshotName, bindAttempt.Matched)...)
		}
		thenBranch = append(thenBranch, &ast.VarDeclStmt{Position: binding.Position, Name: binding.Name, Value: bindAttempt.Value})
	}
	thenBranch = append(thenBranch, &ast.AssignStmt{Position: arm.Position, Target: &ast.Ident{Position: arm.Position, Name: leftName}, Value: arm.Value})
	fallback := []ast.Stmt{restoreCursorStmt(ctx.cursorReceiver, ctx.cursorField, snapshotName, arm.Position)}
	if len(arms) > 1 {
		fallback = append(fallback, ctx.lowerPrecedenceArms(arms[1:], snapshotName, stopName, matchedName, committedName, leftName)...)
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
		bindAttempt := ctx.lowerAttempt(binding.Term)
		thenBranch = append(thenBranch, bindAttempt.Stmts...)
		thenBranch = append(thenBranch, markCommittedStmts(committedName, binding.Position, bindAttempt.Committed)...)
		if ctx.termCanFail(binding.Term) {
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
	elemType := ctx.inferTermType(term.Elem)
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
	tokenExpr := currentTokenExpr(ctx.tokenReceiver, pos)
	var combined ast.Expr
	for _, stop := range stops {
		stopExpr := lowerListUntilStopExpr(pos, tokenExpr, stop)
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

func lowerListUntilStopExpr(pos lexer.Pos, tokenExpr ast.Expr, stop ast.GrammarTerm) ast.Expr {
	switch n := stop.(type) {
	case *ast.GrammarTokenTerm:
		return grammarTokenMatchExpr(pos, tokenExpr, n.Value)
	case *ast.GrammarTokenKindTerm:
		return grammarTokenKindMatchExpr(pos, tokenExpr, grammarTokenKindExpr(n.Position, n.Kind))
	case *ast.GrammarCallTerm:
		if kindExpr, ok := grammarTokenKindMatcher(n); ok {
			return grammarTokenKindMatchExpr(pos, tokenExpr, kindExpr)
		}
	}
	return nil
}

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
		return &ast.CallExpr{Position: n.Position, Func: &ast.Ident{Position: n.Position, Name: "token"}, Args: []ast.Expr{grammarTokenKindExpr(n.Position, n.Kind)}}
	case *ast.GrammarCallTerm:
		if !n.Explicit && len(n.Args) == 0 {
			return lowerQualifiedCalleeExpr(n.Position, n.Name)
		}
		return &ast.CallExpr{Position: n.Position, Func: lowerQualifiedCalleeExpr(n.Position, n.Name), Args: append([]ast.Expr(nil), n.Args...)}
	default:
		return lowerTermExpr(lowerContext{}, stop)
	}
}

func (ctx *statefulLowerContext) inferTermType(term ast.GrammarTerm) ast.TypeExpr {
	switch n := term.(type) {
	case *ast.GrammarTokenTerm:
		return &ast.NamedType{Position: n.Position, Name: "Token"}
	case *ast.GrammarTokenKindTerm:
		return &ast.NamedType{Position: n.Position, Name: "Token"}
	case *ast.GrammarCallTerm:
		if _, ok := grammarTokenKindMatcher(n); ok {
			return &ast.NamedType{Position: n.Position, Name: "Token"}
		}
		_, production, ok := ctx.resolveGrammarProductionInfo(n)
		if ok {
			return grammarResolvedValueTypeExpr(n.Position, production.ReturnType)
		}
		return nil
	case *ast.GrammarChoiceTerm:
		for _, option := range n.Options {
			if typ := ctx.inferTermType(option); typ != nil {
				return typ
			}
		}
	case *ast.GrammarWhenTerm:
		if typ := ctx.inferTermType(n.Then); typ != nil {
			return typ
		}
		return ctx.inferTermType(n.Else)
	case *ast.GrammarRequiredTerm:
		return ctx.inferTermType(n.Term)
	case *ast.GrammarDelimitedTerm:
		return ctx.inferTermType(n.Body)
	case *ast.GrammarSeqTerm:
		if len(n.Terms) == 0 {
			return builtinTypeExpr(n.Position, "bool")
		}
		return ctx.inferTermType(n.Terms[len(n.Terms)-1])
	case *ast.GrammarLookaheadTerm:
		return ctx.inferTermType(n.Term)
	case *ast.GrammarExprTerm:
		return nil
	case *ast.GrammarRecoverTerm:
		return ctx.inferTermType(n.Term)
	case *ast.GrammarGuardTerm:
		return builtinTypeExpr(n.Position, "bool")
	case *ast.GrammarAttemptTerm:
		return nil
	case *ast.GrammarCutTerm:
		return builtinTypeExpr(n.Position, "bool")
	case *ast.GrammarOptionalTerm:
		return ctx.inferTermType(n.Term)
	case *ast.GrammarListTerm:
		return listTypeExpr(n.Position, ctx.inferTermType(n.Elem))
	case *ast.GrammarRepeatTerm:
		return listTypeExpr(n.Position, ctx.inferTermType(n.Elem))
	case *ast.GrammarFlatRepeatTerm:
		return ctx.inferTermType(n.Elem)
	case *ast.GrammarSeparatedTerm:
		return listTypeExpr(n.Position, ctx.inferTermType(n.Elem))
	case *ast.GrammarSuffixTerm:
		return ctx.inferTermType(n.Seed)
	case *ast.GrammarPostfixTerm:
		return ctx.inferTermType(n.Seed)
	case *ast.GrammarPrecedenceTerm:
		if len(n.Levels) != 0 {
			return grammarResolvedValueTypeExpr(n.Position, ctx.production.ReturnType)
		}
		return ctx.inferTermType(n.Seed)
	case *ast.GrammarBindTerm:
		return ctx.inferTermType(n.Term)
	case *ast.GrammarPassTerm:
		return grammarResolvedValueTypeExpr(n.Position, ctx.production.ReturnType)
	}
	return grammarResolvedValueTypeExpr(ctx.production.Position, ctx.production.ReturnType)
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
	ctx := lowerContext{tokenReceiver: receiver.tokenReceiver}
	body := make([]ast.Stmt, 0, len(production.Terms)+1)
	for _, term := range production.Terms {
		body = append(body, lowerTermStmt(ctx, term))
	}
	if production.ReturnType != nil && !grammarTermBlockHasExplicitReturn(production.Terms) {
		body = append(body, &ast.ReturnStmt{Position: production.Position, Value: &ast.CastExpr{Position: production.Position, Operand: &ast.ZeroedLit{Position: production.Position}, Target: production.ReturnType}})
	}
	return &ast.FuncDecl{
		Position:         production.Position,
		Name:             production.Name,
		TypeParams:       append([]string(nil), grammarDecl.TypeParams...),
		RefStorageParams: append([]string(nil), grammarDecl.RefStorageParams...),
		RefStateParams:   append([]string(nil), grammarDecl.RefStateParams...),
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
		return &ast.ReturnStmt{Position: n.Position, Value: n.Value}
	default:
		return &ast.ExprStmt{Position: term.Pos(), Expr: lowerTermExpr(ctx, term)}
	}
}

func lowerTermExpr(ctx lowerContext, term ast.GrammarTerm) ast.Expr {
	switch n := term.(type) {
	case *ast.GrammarTokenTerm:
		funcExpr := grammarExpectFuncExpr(n.Position, ctx.tokenReceiver, "expect")
		return &ast.CallExpr{
			Position: n.Position,
			Func:     funcExpr,
			Args:     []ast.Expr{&ast.StringLit{Position: n.Position, Value: n.Value}},
		}
	case *ast.GrammarTokenKindTerm:
		return &ast.CallExpr{
			Position: n.Position,
			Func:     grammarExpectFuncExpr(n.Position, ctx.tokenReceiver, "expect_kind"),
			Args:     []ast.Expr{grammarTokenKindExpr(n.Position, n.Kind)},
		}
	case *ast.GrammarCallTerm:
		if kindExpr, ok := grammarTokenKindMatcher(n); ok {
			return &ast.CallExpr{
				Position: n.Position,
				Func:     grammarExpectFuncExpr(n.Position, ctx.tokenReceiver, "expect_kind"),
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
		return &ast.TernaryExpr{Position: n.Position, Value: lowerTermExpr(ctx, n.Then), Cond: n.Cond, Alt: lowerTermExpr(ctx, n.Else)}
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
		return n.Expr
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
		return n.Value
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
