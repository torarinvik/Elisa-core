package grammar

import "elisacore/src/ast"

type lowerContext struct {
	tokenReceiver  string
	tokenKindType  ast.TypeExpr
	tokenKindField string
	expectFunc     string
	expectKindFunc string
	eofExpr        ast.Expr
	allocExpr      ast.Expr
	returnType     ast.TypeExpr
	tempCounter    *int
}

func (ctx lowerContext) fresh(prefix string) string {
	if ctx.tempCounter == nil {
		return "__grammar_" + prefix
	}
	*ctx.tempCounter = *ctx.tempCounter + 1
	return "__grammar_" + prefix + "_" + itoa(*ctx.tempCounter)
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
	grammarName     string
	cursorReceiver  string
	cursorField     string
	tokenReceiver   string
	tokenType       ast.TypeExpr
	tokenKindType   ast.TypeExpr
	tokenKindField  string
	tokenLookupFunc string
	currentFunc     string
	advanceFunc     string
	expectFunc      string
	expectKindFunc  string
	recordErrorFunc string
	eofExpr         ast.Expr
	allocName       string
	allocExpr       ast.Expr
	committedName   string
	channels        []ast.GrammarChannelDecl
	production      ast.GrammarProductionDecl
	productionMap   map[string]resolvedGrammarProduction
	structScope     map[string]*ast.StructDecl
	termFactCache   map[ast.GrammarTerm]grammarTermFacts
	tempCounter     int
}

func LowerFile(file *ast.File) *ast.File {
	if file == nil {
		return nil
	}
	return &ast.File{Filename: file.Filename, Decls: lowerDeclList(file.Decls)}
}
func LowerFileStandalone(file *ast.File) *ast.File {
	if file == nil {
		return nil
	}
	return &ast.File{Filename: file.Filename, Decls: lowerDeclListStandalone(file.Decls)}
}
func lowerDeclList(decls []ast.Decl) []ast.Decl {
	return lowerDeclListInScope(decls, grammarDeclScope(decls), grammarEnvDeclScope(decls), structDeclScope(decls), true)
}
func lowerDeclListStandalone(decls []ast.Decl) []ast.Decl {
	return lowerDeclListInScope(decls, grammarDeclScope(decls), grammarEnvDeclScope(decls), structDeclScope(decls), false)
}
func lowerDeclListInScope(decls []ast.Decl, grammarScope map[string]*ast.GrammarDecl, envScope map[string]*ast.GrammarEnvDecl, structScope map[string]*ast.StructDecl, preserveGrammarDecls bool) []ast.Decl {
	if len(decls) == 0 {
		return nil
	}
	lowered := make([]ast.Decl, 0, len(decls))
	loweredGrammarNames := make(map[string]bool)
	for _, decl := range decls {
		switch n := decl.(type) {
		case *ast.GrammarDecl:
			if preserveGrammarDecls {
				lowered = append(lowered, n)
			}
			if loweredGrammarNames[n.Name] {
				continue
			}
			loweredGrammarNames[n.Name] = true
			merged := grammarScope[n.Name]
			if merged == nil {
				merged = n
			}
			lowered = append(lowered, lowerGrammarDecls(merged, grammarScope, envScope, structScope)...)
		case *ast.GrammarEnvDecl:
			continue
		case *ast.LexerDecl:
			lowered = append(lowered, lowerLexerDecls(n, grammarScope, envScope)...)
		case *ast.KeywordMapDecl:
			lowered = append(lowered, lowerKeywordMapDecl(n))
		case *ast.NamespaceDecl:
			cloned := &ast.NamespaceDecl{Position: n.Position, Name: n.Name, Decls: lowerDeclListInScope(n.Decls, grammarDeclScopeForNamespace(n.Decls, n.Name), grammarEnvDeclScope(n.Decls), structDeclScope(n.Decls), preserveGrammarDecls)}
			lowered = append(lowered, cloned)
		default:
			lowered = append(lowered, decl)
		}
	}
	return lowered
}
func structDeclScope(decls []ast.Decl) map[string]*ast.StructDecl {
	if len(decls) == 0 {
		return nil
	}
	scope := make(map[string]*ast.StructDecl)
	for _, decl := range decls {
		structDecl, ok := decl.(*ast.StructDecl)
		if !ok || structDecl == nil || structDecl.Name == "" {
			continue
		}
		scope[structDecl.Name] = structDecl
	}
	return scope
}
func grammarDeclScope(decls []ast.Decl) map[string]*ast.GrammarDecl {
	if len(decls) == 0 {
		return nil
	}
	scope := make(map[string]*ast.GrammarDecl)
	collectGrammarDeclScope(scope, decls, "", true)
	if len(scope) == 0 {
		return nil
	}
	return scope
}
func grammarDeclScopeForNamespace(decls []ast.Decl, namespace string) map[string]*ast.GrammarDecl {
	if len(decls) == 0 {
		return nil
	}
	scope := make(map[string]*ast.GrammarDecl)
	collectGrammarDeclScope(scope, decls, namespace, true)
	if len(scope) == 0 {
		return nil
	}
	return scope
}
func collectGrammarDeclScope(scope map[string]*ast.GrammarDecl, decls []ast.Decl, namespace string, includeLocalNames bool) {
	for _, decl := range decls {
		switch n := decl.(type) {
		case *ast.GrammarDecl:
			if n == nil || n.Name == "" {
				continue
			}
			if includeLocalNames {
				scope[n.Name] = mergeGrammarDecls(scope[n.Name], n)
			}
			if namespace != "" {
				qualified := namespace + "." + n.Name
				scope[qualified] = mergeGrammarDecls(scope[qualified], n)
			}
		case *ast.NamespaceDecl:
			if n == nil || n.Name == "" {
				continue
			}
			childNamespace := n.Name
			if namespace != "" {
				childNamespace = namespace + "." + n.Name
			}
			collectGrammarDeclScope(scope, n.Decls, childNamespace, false)
		}
	}
}
func grammarEnvDeclScope(decls []ast.Decl) map[string]*ast.GrammarEnvDecl {
	if len(decls) == 0 {
		return nil
	}
	scope := make(map[string]*ast.GrammarEnvDecl)
	for _, decl := range decls {
		envDecl, ok := decl.(*ast.GrammarEnvDecl)
		if !ok || envDecl == nil || envDecl.Name == "" {
			continue
		}
		scope[envDecl.Name] = envDecl
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
	cloned.EnvType = decl.EnvType
	cloned.Uses = append([]ast.TypeExpr(nil), decl.Uses...)
	cloned.TokenAliases = append([]ast.GrammarTokenAliasDecl(nil), decl.TokenAliases...)
	cloned.Channels = append([]ast.GrammarChannelDecl(nil), decl.Channels...)
	cloned.TokenSets = append([]ast.GrammarTokenSetDecl(nil), decl.TokenSets...)
	cloned.GrammarAliases = append([]ast.GrammarAliasDecl(nil), decl.GrammarAliases...)
	cloned.GrammarFns = append([]ast.GrammarFnDecl(nil), decl.GrammarFns...)
	cloned.RecoveryPolicies = append([]ast.GrammarRecoveryDecl(nil), decl.RecoveryPolicies...)
	cloned.InfixTables = append([]ast.GrammarInfixTableDecl(nil), decl.InfixTables...)
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
	if merged.EnvType == nil {
		merged.EnvType = extra.EnvType
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
	if merged.TokenKindType == nil {
		merged.TokenKindType = extra.TokenKindType
	}
	if merged.TokenEnumName == "" {
		merged.TokenEnumName = extra.TokenEnumName
	}
	if merged.TokenEnumStorage == nil {
		merged.TokenEnumStorage = extra.TokenEnumStorage
	}
	if merged.EOFExpr == nil {
		merged.EOFExpr = extra.EOFExpr
	}
	if merged.TokenKindField == "" {
		merged.TokenKindField = extra.TokenKindField
	}
	if merged.CurrentFunc == "" {
		merged.CurrentFunc = extra.CurrentFunc
	}
	if merged.AdvanceFunc == "" {
		merged.AdvanceFunc = extra.AdvanceFunc
	}
	if merged.ExpectFunc == "" {
		merged.ExpectFunc = extra.ExpectFunc
	}
	if merged.ExpectKindFunc == "" {
		merged.ExpectKindFunc = extra.ExpectKindFunc
	}
	if merged.RecordErrorFunc == "" {
		merged.RecordErrorFunc = extra.RecordErrorFunc
	}
	if merged.TokenLookupFunc == "" {
		merged.TokenLookupFunc = extra.TokenLookupFunc
	}
	if merged.TokenLookupCompareFunc == "" {
		merged.TokenLookupCompareFunc = extra.TokenLookupCompareFunc
	}
	merged.TokenAliases = append(merged.TokenAliases, extra.TokenAliases...)
	merged.Channels = append(merged.Channels, extra.Channels...)
	merged.TokenSets = append(merged.TokenSets, extra.TokenSets...)
	merged.GrammarAliases = append(merged.GrammarAliases, extra.GrammarAliases...)
	merged.GrammarFns = append(merged.GrammarFns, extra.GrammarFns...)
	merged.RecoveryPolicies = append(merged.RecoveryPolicies, extra.RecoveryPolicies...)
	merged.InfixTables = append(merged.InfixTables, extra.InfixTables...)
	merged.Productions = append(merged.Productions, extra.Productions...)
	merged.Extend = false
	return merged
}
func lowerGrammarDecls(decl *ast.GrammarDecl, grammarScope map[string]*ast.GrammarDecl, envScope map[string]*ast.GrammarEnvDecl, structScope map[string]*ast.StructDecl) []ast.Decl {
	if decl == nil {
		return nil
	}
	normalizedDecl := normalizeGrammarDeclForLoweringInScope(decl, grammarScope, envScope)
	if !grammarDeclIsStateful(normalizedDecl) {
		public := LowerDecl(normalizedDecl)
		out := make([]ast.Decl, 0, len(public)+2)
		if tokenEnum := lowerGrammarTokenEnumDecl(normalizedDecl); tokenEnum != nil {
			out = append(out, tokenEnum)
		}
		if lookup := lowerGrammarTokenLookupFunc(normalizedDecl); lookup != nil {
			out = append(out, lookup)
		}
		if lookupAssert := lowerGrammarTokenLookupAssertFunc(normalizedDecl); lookupAssert != nil {
			out = append(out, lookupAssert)
		}
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
	productionMap := reachableGrammarProductionMap(normalizedDecl, grammarScope, envScope, rewrittenProductions, helperProductions)
	out := make([]ast.Decl, 0, len(rewrittenProductions)*3+len(helperProductions)+2)
	if tokenEnum := lowerGrammarTokenEnumDecl(normalizedDecl); tokenEnum != nil {
		out = append(out, tokenEnum)
	}
	if lookup := lowerGrammarTokenLookupFunc(normalizedDecl); lookup != nil {
		out = append(out, lookup)
	}
	if lookupAssert := lowerGrammarTokenLookupAssertFunc(normalizedDecl); lookupAssert != nil {
		out = append(out, lookupAssert)
	}
	for _, production := range rewrittenProductions {
		receiver := grammarReceiverInfoForProduction(normalizedDecl, production)
		ctx := &statefulLowerContext{
			grammarName:     normalizedDecl.Name,
			cursorReceiver:  receiver.cursorReceiver,
			cursorField:     receiver.cursorField,
			tokenReceiver:   receiver.tokenReceiver,
			tokenType:       grammarDeclTokenType(normalizedDecl, production.Position),
			tokenKindType:   grammarDeclTokenKindType(normalizedDecl, production.Position),
			tokenKindField:  grammarDeclTokenKindField(normalizedDecl),
			tokenLookupFunc: grammarDeclTokenLookupFunc(normalizedDecl),
			currentFunc:     grammarDeclCurrentFunc(normalizedDecl),
			advanceFunc:     grammarDeclAdvanceFunc(normalizedDecl),
			expectFunc:      grammarDeclExpectFunc(normalizedDecl),
			expectKindFunc:  grammarDeclExpectKindFunc(normalizedDecl),
			recordErrorFunc: grammarDeclRecordErrorFunc(normalizedDecl),
			eofExpr:         grammarDeclEOFExpr(normalizedDecl, production.Position),
			allocName:       grammarAllocNameForProduction(normalizedDecl, production),
			allocExpr:       grammarAllocExprForProduction(normalizedDecl, production, receiver, production.Position),
			channels:        grammarProductionChannels(normalizedDecl, production),
			production:      production,
			productionMap:   productionMap,
			structScope:     structScope,
			termFactCache:   make(map[ast.GrammarTerm]grammarTermFacts),
		}
		out = append(out, lowerStatefulPublicProduction(normalizedDecl, ctx))
		out = append(out, lowerStatefulPublicTryProduction(normalizedDecl, ctx))
		out = append(out, lowerStatefulTryProduction(normalizedDecl, ctx))
	}
	for _, production := range helperProductions {
		receiver := grammarReceiverInfoForProduction(normalizedDecl, production)
		ctx := &statefulLowerContext{
			grammarName:     normalizedDecl.Name,
			cursorReceiver:  receiver.cursorReceiver,
			cursorField:     receiver.cursorField,
			tokenReceiver:   receiver.tokenReceiver,
			tokenType:       grammarDeclTokenType(normalizedDecl, production.Position),
			tokenKindType:   grammarDeclTokenKindType(normalizedDecl, production.Position),
			tokenKindField:  grammarDeclTokenKindField(normalizedDecl),
			tokenLookupFunc: grammarDeclTokenLookupFunc(normalizedDecl),
			currentFunc:     grammarDeclCurrentFunc(normalizedDecl),
			advanceFunc:     grammarDeclAdvanceFunc(normalizedDecl),
			expectFunc:      grammarDeclExpectFunc(normalizedDecl),
			expectKindFunc:  grammarDeclExpectKindFunc(normalizedDecl),
			recordErrorFunc: grammarDeclRecordErrorFunc(normalizedDecl),
			eofExpr:         grammarDeclEOFExpr(normalizedDecl, production.Position),
			allocName:       grammarAllocNameForProduction(normalizedDecl, production),
			allocExpr:       grammarAllocExprForProduction(normalizedDecl, production, receiver, production.Position),
			channels:        grammarProductionChannels(normalizedDecl, production),
			production:      production,
			productionMap:   productionMap,
			structScope:     structScope,
			termFactCache:   make(map[ast.GrammarTerm]grammarTermFacts),
		}
		out = append(out, lowerStatefulTryProduction(normalizedDecl, ctx))
	}
	return out
}
func grammarProductionChannels(grammarDecl *ast.GrammarDecl, production ast.GrammarProductionDecl) []ast.GrammarChannelDecl {
	count := len(production.Channels)
	if grammarDecl != nil {
		count += len(grammarDecl.Channels)
	}
	if count == 0 {
		return nil
	}
	channels := make([]ast.GrammarChannelDecl, 0, count)
	if grammarDecl != nil {
		channels = append(channels, grammarDecl.Channels...)
	}
	channels = append(channels, production.Channels...)
	return channels
}
func reachableGrammarProductionMap(grammarDecl *ast.GrammarDecl, grammarScope map[string]*ast.GrammarDecl, envScope map[string]*ast.GrammarEnvDecl, localProductions []ast.GrammarProductionDecl, helperProductions []ast.GrammarProductionDecl) map[string]resolvedGrammarProduction {
	resolved := make(map[string]resolvedGrammarProduction, len(localProductions)+len(helperProductions))
	for _, production := range localProductions {
		resolved[production.Name] = resolvedGrammarProduction{GrammarName: grammarDecl.Name, Production: production, TryName: grammarTryFuncName(grammarDecl.Name, production.Name)}
	}
	for _, production := range helperProductions {
		resolved[production.Name] = resolvedGrammarProduction{GrammarName: grammarDecl.Name, Production: production, TryName: grammarTryFuncName(grammarDecl.Name, production.Name)}
	}
	seen := map[string]bool{grammarDecl.Name: true}
	appendUsedGrammarProductions(resolved, grammarDecl, grammarScope, envScope, seen)
	return resolved
}
func appendUsedGrammarProductions(resolved map[string]resolvedGrammarProduction, grammarDecl *ast.GrammarDecl, grammarScope map[string]*ast.GrammarDecl, envScope map[string]*ast.GrammarEnvDecl, seen map[string]bool) {
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
		normalizedUsed := normalizeGrammarDeclBeforeFirstSetsInScope(usedDecl, grammarScope, envScope)
		for _, production := range normalizedUsed.Productions {
			if _, exists := resolved[production.Name]; exists {
				continue
			}
			resolved[production.Name] = resolvedGrammarProduction{GrammarName: normalizedUsed.Name, Production: production, TryName: grammarTryFuncName(normalizedUsed.Name, production.Name)}
		}
		appendUsedGrammarProductions(resolved, normalizedUsed, grammarScope, envScope, seen)
	}
}
func appendUsedGrammarSupportDecls(tokenAliases *[]ast.GrammarTokenAliasDecl, tokenSets *[]ast.GrammarTokenSetDecl, grammarAliases *[]ast.GrammarAliasDecl, grammarFns *[]ast.GrammarFnDecl, recoveryPolicies *[]ast.GrammarRecoveryDecl, infixTables *[]ast.GrammarInfixTableDecl, grammarDecl *ast.GrammarDecl, grammarScope map[string]*ast.GrammarDecl, seen map[string]bool) {
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
		appendUsedGrammarSupportDecls(tokenAliases, tokenSets, grammarAliases, grammarFns, recoveryPolicies, infixTables, usedDecl, grammarScope, seen)
		*tokenAliases = append(*tokenAliases, usedDecl.TokenAliases...)
		*tokenSets = append(*tokenSets, usedDecl.TokenSets...)
		*grammarAliases = append(*grammarAliases, usedDecl.GrammarAliases...)
		*grammarFns = append(*grammarFns, usedDecl.GrammarFns...)
		*recoveryPolicies = append(*recoveryPolicies, usedDecl.RecoveryPolicies...)
		*infixTables = append(*infixTables, usedDecl.InfixTables...)
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
func grammarEnvName(typ ast.TypeExpr) string {
	return grammarUseName(typ)
}
func normalizeGrammarDeclForLowering(decl *ast.GrammarDecl) *ast.GrammarDecl {
	return normalizeGrammarDeclForLoweringInScope(decl, nil, nil)
}
func normalizeGrammarDeclBeforeFirstSetsInScope(decl *ast.GrammarDecl, grammarScope map[string]*ast.GrammarDecl, envScope map[string]*ast.GrammarEnvDecl) *ast.GrammarDecl {
	if decl == nil {
		return nil
	}
	normalizedDecl := desugarGrammarDeclWhileTerms(applyGrammarEnvDefaults(decl, envScope))
	normalized := *normalizedDecl
	normalized.TokenAliases = nil
	normalized.TokenSets = nil
	normalized.GrammarAliases = nil
	normalized.GrammarFns = nil
	normalized.RecoveryPolicies = nil
	normalized.InfixTables = nil
	if len(normalizedDecl.Uses) != 0 && grammarScope != nil {
		seen := map[string]bool{normalizedDecl.Name: true}
		appendUsedGrammarSupportDecls(&normalized.TokenAliases, &normalized.TokenSets, &normalized.GrammarAliases, &normalized.GrammarFns, &normalized.RecoveryPolicies, &normalized.InfixTables, normalizedDecl, grammarScope, seen)
	}
	normalized.TokenAliases = append(normalized.TokenAliases, normalizedDecl.TokenAliases...)
	normalized.TokenSets = append(normalized.TokenSets, normalizedDecl.TokenSets...)
	normalized.GrammarAliases = append(normalized.GrammarAliases, normalizedDecl.GrammarAliases...)
	normalized.GrammarFns = append(normalized.GrammarFns, normalizedDecl.GrammarFns...)
	normalized.RecoveryPolicies = append(normalized.RecoveryPolicies, normalizedDecl.RecoveryPolicies...)
	normalized.InfixTables = append(normalized.InfixTables, normalizedDecl.InfixTables...)
	aliasByLiteral := grammarTokenAliasLiteralMap(normalized.TokenAliases)
	normalized.TokenSets = rewriteGrammarTokenSetsTokenAliases(normalized.TokenSets, aliasByLiteral)
	normalized.RecoveryPolicies = rewriteGrammarRecoveryPoliciesTokenAliases(normalized.RecoveryPolicies, aliasByLiteral)
	normalized.InfixTables = rewriteGrammarInfixTablesTokenAliases(normalized.InfixTables, aliasByLiteral)
	tokenSets := grammarTokenSetMap(normalized.TokenSets)
	normalized.TokenSets = rewriteGrammarTokenSetBareRefsToKinds(normalized.TokenSets, tokenSets)
	tokenSets = grammarTokenSetMap(normalized.TokenSets)
	normalized.TokenSets = resolveGrammarTokenSetsTokenSets(normalized.TokenSets, tokenSets)
	tokenSets = grammarTokenSetMap(normalized.TokenSets)
	normalized.RecoveryPolicies = resolveGrammarRecoveryPoliciesTokenSets(normalized.RecoveryPolicies, tokenSets)
	grammarAliases := grammarAliasMap(normalized.GrammarAliases)
	normalized.GrammarAliases = expandGrammarAliasesGrammarAliases(normalized.GrammarAliases, grammarAliases)
	grammarAliases = grammarAliasMap(normalized.GrammarAliases)
	normalized.InfixTables = expandGrammarInfixTablesGrammarAliases(normalized.InfixTables, grammarAliases)
	normalized.GrammarFns = expandGrammarFnsGrammarAliases(normalized.GrammarFns, grammarAliases)
	grammarFns := grammarFnMap(normalized.GrammarFns)
	grammarFns = addParameterizedGrammarAliasesToGrammarFnMap(grammarFns, normalized.GrammarAliases)
	normalized.InfixTables = expandGrammarInfixTablesGrammarFns(normalized.InfixTables, grammarFns)
	normalized.InfixTables = rewriteGrammarInfixTablesTokenAliases(normalized.InfixTables, aliasByLiteral)
	recoveryPolicies := grammarRecoveryPolicyMap(normalized.RecoveryPolicies)
	infixTables := grammarInfixTableMap(normalized.InfixTables)
	normalized.Productions = make([]ast.GrammarProductionDecl, 0, len(normalizedDecl.Productions))
	for _, production := range normalizedDecl.Productions {
		rewritten := expandGrammarProductionGrammarAliases(production, grammarAliases)
		rewritten = expandGrammarProductionGrammarFns(rewritten, grammarFns)
		rewritten = rewriteGrammarProductionTokenAliases(rewritten, aliasByLiteral)
		rewritten = resolveGrammarProductionTokenSets(rewritten, tokenSets)
		rewritten = resolveGrammarProductionRecoveryPolicies(rewritten, recoveryPolicies)
		rewritten = resolveGrammarProductionInfixTables(rewritten, infixTables)
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
