package grammar

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func desugarGrammarDeclWhileTerms(decl *ast.GrammarDecl) *ast.GrammarDecl {
	if decl == nil {
		return nil
	}
	rewritten := cloneGrammarDecl(decl)
	if len(rewritten.GrammarAliases) != 0 {
		aliases := make([]ast.GrammarAliasDecl, 0, len(rewritten.GrammarAliases))
		for _, alias := range rewritten.GrammarAliases {
			alias.Term = desugarGrammarWhileTerm(alias.Term)
			aliases = append(aliases, alias)
		}
		rewritten.GrammarAliases = aliases
	}
	if len(rewritten.GrammarFns) != 0 {
		grammarFns := make([]ast.GrammarFnDecl, 0, len(rewritten.GrammarFns))
		for _, grammarFn := range rewritten.GrammarFns {
			if len(grammarFn.Params) != 0 {
				params := make([]ast.GrammarFnParam, 0, len(grammarFn.Params))
				for _, param := range grammarFn.Params {
					param.Default = desugarGrammarWhileTerm(param.Default)
					params = append(params, param)
				}
				grammarFn.Params = params
			}
			grammarFn.Terms = desugarGrammarWhileTerms(grammarFn.Terms)
			grammarFns = append(grammarFns, grammarFn)
		}
		rewritten.GrammarFns = grammarFns
	}
	if len(rewritten.RecoveryPolicies) != 0 {
		policies := make([]ast.GrammarRecoveryDecl, 0, len(rewritten.RecoveryPolicies))
		for _, policy := range rewritten.RecoveryPolicies {
			policy.Until = desugarGrammarWhileTerms(policy.Until)
			policies = append(policies, policy)
		}
		rewritten.RecoveryPolicies = policies
	}
	if len(rewritten.InfixTables) != 0 {
		tables := make([]ast.GrammarInfixTableDecl, 0, len(rewritten.InfixTables))
		for _, table := range rewritten.InfixTables {
			if len(table.Levels) != 0 {
				levels := make([]ast.GrammarPrecedenceLevel, 0, len(table.Levels))
				for _, level := range table.Levels {
					levels = append(levels, desugarGrammarWhilePrecedenceLevel(level))
				}
				table.Levels = levels
			}
			tables = append(tables, table)
		}
		rewritten.InfixTables = tables
	}
	if len(rewritten.Productions) != 0 {
		productions := make([]ast.GrammarProductionDecl, 0, len(rewritten.Productions))
		for _, production := range rewritten.Productions {
			production.RecoverUntil = desugarGrammarWhileTerms(production.RecoverUntil)
			production.Terms = desugarGrammarWhileTerms(production.Terms)
			productions = append(productions, production)
		}
		rewritten.Productions = productions
	}
	return rewritten
}
func desugarGrammarWhileTerms(terms []ast.GrammarTerm) []ast.GrammarTerm {
	if len(terms) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarTerm, 0, len(terms))
	for _, term := range terms {
		rewritten = append(rewritten, desugarGrammarWhileTerm(term))
	}
	return rewritten
}
func desugarGrammarWhileTerm(term ast.GrammarTerm) ast.GrammarTerm {
	if term == nil {
		return nil
	}
	switch n := term.(type) {
	case *ast.GrammarChoiceTerm:
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: desugarGrammarWhileTerms(n.Options)}
	case *ast.GrammarOptionalTerm:
		return &ast.GrammarOptionalTerm{Position: n.Position, Term: desugarGrammarWhileTerm(n.Term)}
	case *ast.GrammarWhenTerm:
		return &ast.GrammarWhenTerm{Position: n.Position, Cond: n.Cond, TokenKindGate: n.TokenKindGate, Then: desugarGrammarWhileTerm(n.Then), Else: desugarGrammarWhileTerm(n.Else)}
	case *ast.GrammarMatchTerm:
		return desugarGrammarMatchTerm(n)
	case *ast.GrammarRecoverTerm:
		return &ast.GrammarRecoverTerm{Position: n.Position, Term: desugarGrammarWhileTerm(n.Term), RecoverPolicy: n.RecoverPolicy, RecoverMsg: n.RecoverMsg, RecoverUntil: desugarGrammarWhileTerms(n.RecoverUntil), RecoverValue: n.RecoverValue}
	case *ast.GrammarRequiredTerm:
		return &ast.GrammarRequiredTerm{Position: n.Position, Term: desugarGrammarWhileTerm(n.Term), Message: n.Message}
	case *ast.GrammarDelimitedTerm:
		return &ast.GrammarDelimitedTerm{Position: n.Position, Open: desugarGrammarWhileTerm(n.Open), Body: desugarGrammarWhileTerm(n.Body), Close: desugarGrammarWhileTerm(n.Close), Message: n.Message}
	case *ast.GrammarSeqTerm:
		return &ast.GrammarSeqTerm{Position: n.Position, Terms: desugarGrammarWhileTerms(n.Terms)}
	case *ast.GrammarLookaheadTerm:
		return &ast.GrammarLookaheadTerm{Position: n.Position, Term: desugarGrammarWhileTerm(n.Term)}
	case *ast.GrammarConcatTerm:
		return &ast.GrammarConcatTerm{Position: n.Position, Terms: desugarGrammarWhileTerms(n.Terms)}
	case *ast.GrammarListTerm:
		return &ast.GrammarListTerm{Position: n.Position, Elem: desugarGrammarWhileTerm(n.Elem), Separator: desugarGrammarWhileTerm(n.Separator), Until: desugarGrammarWhileTerms(n.Until)}
	case *ast.GrammarRepeatTerm:
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: desugarGrammarWhileTerm(n.Elem), Until: desugarGrammarWhileTerms(n.Until)}
	case *ast.GrammarFlatRepeatTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: desugarGrammarWhileTerm(n.Elem), Until: desugarGrammarWhileTerms(n.Until)}
	case *ast.GrammarWhileTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: desugarGrammarWhileTerm(n.Elem), Until: desugarGrammarWhileTerms(n.Until)}
	case *ast.GrammarSeparatedTerm:
		return &ast.GrammarSeparatedTerm{Position: n.Position, Elem: desugarGrammarWhileTerm(n.Elem), Separator: desugarGrammarWhileTerm(n.Separator), Until: desugarGrammarWhileTerms(n.Until)}
	case *ast.GrammarSuffixTerm:
		return &ast.GrammarSuffixTerm{Position: n.Position, LeftName: n.LeftName, Seed: desugarGrammarWhileTerm(n.Seed), Arms: desugarGrammarWhilePostfixArms(n.Arms)}
	case *ast.GrammarPostfixTerm:
		return &ast.GrammarPostfixTerm{Position: n.Position, LeftName: n.LeftName, Seed: desugarGrammarWhileTerm(n.Seed), Arms: desugarGrammarWhilePostfixArms(n.Arms)}
	case *ast.GrammarPrecedenceTerm:
		levels := make([]ast.GrammarPrecedenceLevel, 0, len(n.Levels))
		for _, level := range n.Levels {
			levels = append(levels, desugarGrammarWhilePrecedenceLevel(level))
		}
		return &ast.GrammarPrecedenceTerm{Position: n.Position, Assoc: n.Assoc, Result: n.Result, Levels: levels, LeftName: n.LeftName, Seed: desugarGrammarWhileTerm(n.Seed), Arms: desugarGrammarWhilePrecedenceArms(n.Arms)}
	case *ast.GrammarBindTerm:
		return &ast.GrammarBindTerm{Position: n.Position, Name: n.Name, Term: desugarGrammarWhileTerm(n.Term)}
	case *ast.GrammarAssignTerm:
		return &ast.GrammarAssignTerm{Position: n.Position, Name: n.Name, Term: desugarGrammarWhileTerm(n.Term)}
	case *ast.GrammarReturnTerm:
		return &ast.GrammarReturnTerm{Position: n.Position, Term: desugarGrammarWhileTerm(n.Term)}
	default:
		return term
	}
}
func desugarGrammarMatchTerm(term *ast.GrammarMatchTerm) ast.GrammarTerm {
	if term == nil {
		return nil
	}
	type dispatchArm struct {
		position lexer.Pos
		patterns []ast.MatchPattern
		term     ast.GrammarTerm
	}
	arms := make([]dispatchArm, 0, len(term.Arms))
	var fallback ast.GrammarTerm
	for _, arm := range term.Arms {
		rewrittenTerm := desugarGrammarWhileTerm(arm.Term)
		wildcard := false
		patterns := make([]ast.MatchPattern, 0, len(arm.Patterns))
		for _, pattern := range arm.Patterns {
			if _, ok := pattern.(*ast.MatchWildcardPattern); ok {
				wildcard = true
				continue
			}
			patterns = append(patterns, pattern)
		}
		if wildcard {
			fallback = rewrittenTerm
		}
		if len(patterns) != 0 {
			arms = append(arms, dispatchArm{position: arm.Position, patterns: patterns, term: rewrittenTerm})
		}
	}
	if fallback == nil {
		fallback = &ast.GrammarEmptyTerm{Position: term.Position}
	}
	result := fallback
	for i := len(arms) - 1; i >= 0; i-- {
		arm := arms[i]
		result = &ast.GrammarWhenTerm{
			Position: arm.position,
			Cond:     grammarMatchPatternsCond(term.Value, arm.patterns),
			Then:     arm.term,
			Else:     result,
		}
	}
	return result
}
func grammarMatchPatternsCond(value ast.Expr, patterns []ast.MatchPattern) ast.Expr {
	if len(patterns) == 0 {
		return &ast.BoolLit{Position: value.Pos(), Value: false}
	}
	cond := grammarMatchPatternCond(value, patterns[0])
	for _, pattern := range patterns[1:] {
		cond = &ast.BinaryExpr{
			Position: pattern.Pos(),
			Op:       lexer.TOKEN_OR,
			Left:     cond,
			Right:    grammarMatchPatternCond(value, pattern),
		}
	}
	return cond
}
func grammarMatchPatternCond(value ast.Expr, pattern ast.MatchPattern) ast.Expr {
	switch n := pattern.(type) {
	case *ast.MatchVariantPattern:
		if len(n.Args) != 0 {
			return &ast.BoolLit{Position: n.Position, Value: false}
		}
		return &ast.BinaryExpr{
			Position: n.Position,
			Op:       lexer.TOKEN_EQEQ,
			Left:     value,
			Right:    lowerQualifiedCalleeExpr(n.Position, n.EnumName+"."+n.Variant),
		}
	case *ast.MatchStringLiteralPattern:
		return &ast.BinaryExpr{
			Position: n.Position,
			Op:       lexer.TOKEN_EQEQ,
			Left:     value,
			Right:    &ast.StringLit{Position: n.Position, Value: n.Value},
		}
	case *ast.MatchLiteralPattern:
		return &ast.BinaryExpr{Position: n.Position, Op: lexer.TOKEN_EQEQ, Left: value, Right: n.Value}
	default:
		return &ast.BoolLit{Position: pattern.Pos(), Value: false}
	}
}
func desugarGrammarWhilePostfixArms(arms []ast.GrammarPostfixArm) []ast.GrammarPostfixArm {
	if len(arms) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarPostfixArm, 0, len(arms))
	for _, arm := range arms {
		arm.Op = desugarGrammarWhileTerm(arm.Op)
		arm.Bindings = desugarGrammarWhileBindings(arm.Bindings)
		rewritten = append(rewritten, arm)
	}
	return rewritten
}
func desugarGrammarWhilePrecedenceArms(arms []ast.GrammarPrecedenceArm) []ast.GrammarPrecedenceArm {
	if len(arms) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarPrecedenceArm, 0, len(arms))
	for _, arm := range arms {
		arm.Op = desugarGrammarWhileTerm(arm.Op)
		arm.Bindings = desugarGrammarWhileBindings(arm.Bindings)
		rewritten = append(rewritten, arm)
	}
	return rewritten
}
func desugarGrammarWhilePrecedenceLevel(level ast.GrammarPrecedenceLevel) ast.GrammarPrecedenceLevel {
	level.Seed = desugarGrammarWhileTerm(level.Seed)
	level.Arms = desugarGrammarWhilePrecedenceArms(level.Arms)
	return level
}
func desugarGrammarWhileBindings(bindings []*ast.GrammarBindTerm) []*ast.GrammarBindTerm {
	if len(bindings) == 0 {
		return nil
	}
	rewritten := make([]*ast.GrammarBindTerm, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		rewritten = append(rewritten, &ast.GrammarBindTerm{Position: binding.Position, Name: binding.Name, Term: desugarGrammarWhileTerm(binding.Term)})
	}
	return rewritten
}
func normalizeGrammarDeclForLoweringInScope(decl *ast.GrammarDecl, grammarScope map[string]*ast.GrammarDecl, envScope map[string]*ast.GrammarEnvDecl) *ast.GrammarDecl {
	normalized := normalizeGrammarDeclBeforeFirstSetsInScope(decl, grammarScope, envScope)
	if normalized == nil {
		return nil
	}
	firstProductions := reachableGrammarProductionMap(normalized, grammarScope, envScope, normalized.Productions, nil)
	normalized.TokenSets = resolveGrammarTokenSetsFirstSets(normalized.TokenSets, firstProductions)
	normalized.RecoveryPolicies = resolveGrammarRecoveryPoliciesFirstSets(normalized.RecoveryPolicies, firstProductions)
	finalProductions := make([]ast.GrammarProductionDecl, 0, len(normalized.Productions))
	for _, production := range normalized.Productions {
		finalProductions = append(finalProductions, resolveGrammarProductionFirstSets(production, firstProductions))
	}
	normalized.Productions = finalProductions
	return normalized
}
func applyGrammarEnvDefaults(decl *ast.GrammarDecl, envScope map[string]*ast.GrammarEnvDecl) *ast.GrammarDecl {
	if decl == nil || decl.EnvType == nil || envScope == nil {
		return decl
	}
	envName := grammarEnvName(decl.EnvType)
	envDecl := envScope[envName]
	if envDecl == nil {
		return decl
	}
	applied := cloneGrammarDecl(decl)
	if applied.OverType == nil {
		applied.OverType = envDecl.OverType
	}
	if applied.UsingType == nil {
		applied.UsingType = envDecl.UsingType
	}
	if applied.ErrorType == nil {
		applied.ErrorType = envDecl.ErrorType
	}
	if applied.CursorExpr == nil {
		applied.CursorExpr = envDecl.CursorExpr
	}
	if applied.AllocExpr == nil {
		applied.AllocExpr = envDecl.AllocExpr
	}
	if applied.TokenKindType == nil {
		applied.TokenKindType = envDecl.TokenKindType
	}
	if applied.EOFExpr == nil {
		applied.EOFExpr = envDecl.EOFExpr
	}
	if applied.TokenKindField == "" {
		applied.TokenKindField = envDecl.TokenKindField
	}
	if applied.CurrentFunc == "" {
		applied.CurrentFunc = envDecl.CurrentFunc
	}
	if applied.AdvanceFunc == "" {
		applied.AdvanceFunc = envDecl.AdvanceFunc
	}
	if applied.ExpectFunc == "" {
		applied.ExpectFunc = envDecl.ExpectFunc
	}
	if applied.ExpectKindFunc == "" {
		applied.ExpectKindFunc = envDecl.ExpectKindFunc
	}
	if applied.RecordErrorFunc == "" {
		applied.RecordErrorFunc = envDecl.RecordErrorFunc
	}
	if envDecl.TokenGrammarName != "" {
		applied.Uses = appendGrammarUseIfMissing(applied.Uses, envDecl.TokenGrammarName, envDecl.Position)
	}
	return applied
}
func appendGrammarUseIfMissing(uses []ast.TypeExpr, name string, pos lexer.Pos) []ast.TypeExpr {
	if name == "" {
		return uses
	}
	for _, used := range uses {
		if grammarUseName(used) == name {
			return uses
		}
	}
	return append(uses, &ast.NamedType{Position: pos, Name: name})
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
func lowerGrammarTokenEnumDecl(grammarDecl *ast.GrammarDecl) *ast.ConstEnumDecl {
	if grammarDecl == nil || grammarDecl.TokenEnumName == "" {
		return nil
	}
	pos := grammarDecl.Position
	storage := grammarDecl.TokenEnumStorage
	if storage == nil {
		storage = builtinTypeExpr(pos, "i16")
	}
	members := make([]ast.ConstEnumMemberDecl, 0, len(grammarDecl.TokenAliases)+1)
	seen := make(map[string]bool, len(grammarDecl.TokenAliases)+1)
	addMember := func(memberPos lexer.Pos, name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		members = append(members, ast.ConstEnumMemberDecl{
			Position: memberPos,
			Name:     name,
			Value:    lexerInt(memberPos, len(members)),
		})
	}
	addMember(pos, "EOF")
	for _, alias := range grammarDecl.TokenAliases {
		addMember(alias.Position, alias.Kind)
	}
	return &ast.ConstEnumDecl{Position: pos, Name: grammarDecl.TokenEnumName, Storage: storage, Members: members}
}
func lowerGrammarTokenLookupFunc(grammarDecl *ast.GrammarDecl) *ast.FuncDecl {
	if grammarDecl == nil || grammarDecl.TokenLookupFunc == "" {
		return nil
	}
	aliases := grammarDecl.TokenAliases
	if len(aliases) == 0 {
		return nil
	}
	pos := grammarDecl.Position
	body := make([]ast.Stmt, 0, len(aliases)+1)
	compareFunc := grammarDecl.TokenLookupCompareFunc
	for _, alias := range aliases {
		if !alias.HasLiteral || alias.Literal == "" {
			continue
		}
		cond := grammarTokenLookupCond(alias.Position, compareFunc, alias.Literal)
		body = append(body, &ast.IfStmt{
			Position: alias.Position,
			Cond:     cond,
			Then: []ast.Stmt{
				&ast.ReturnStmt{
					Position: alias.Position,
					Value:    grammarTokenKindExpr(alias.Position, grammarDeclTokenKindType(grammarDecl, alias.Position), alias.Kind),
				},
			},
		})
	}
	if len(body) == 0 {
		return nil
	}
	body = append(body, &ast.ReturnStmt{Position: pos, Value: grammarDeclEOFExpr(grammarDecl, pos)})
	return &ast.FuncDecl{
		Position: pos,
		Name:     grammarDecl.TokenLookupFunc,
		Params: []ast.ParamDecl{
			{Position: pos, Name: "text", Type: builtinTypeExpr(pos, "cstr")},
		},
		ReturnType: grammarDeclTokenKindType(grammarDecl, pos),
		Body:       body,
	}
}
func lowerGrammarTokenLookupAssertFunc(grammarDecl *ast.GrammarDecl) *ast.FuncDecl {
	if grammarDecl == nil || grammarDecl.TokenLookupFunc == "" {
		return nil
	}
	pos := grammarDecl.Position
	body := make([]ast.Stmt, 0, len(grammarDecl.TokenAliases))
	for _, alias := range grammarDecl.TokenAliases {
		if !alias.HasLiteral || alias.Literal == "" {
			continue
		}
		body = append(body, tokenLookupAssertStmt(
			alias.Position,
			&ast.Ident{Position: alias.Position, Name: grammarDecl.TokenLookupFunc},
			[]ast.Expr{&ast.StringLit{Position: alias.Position, Value: alias.Literal}},
			grammarTokenKindExpr(alias.Position, grammarDeclTokenKindType(grammarDecl, alias.Position), alias.Kind),
		))
	}
	if len(body) == 0 {
		return nil
	}
	return &ast.FuncDecl{
		Position:    pos,
		Name:        grammarDecl.TokenLookupFunc + "_assert_table",
		Permissions: []ast.PermissionRef{abortPanicPermission(pos)},
		ReturnType:  builtinTypeExpr(pos, "void"),
		Body:        abortPanicBody(pos, body),
	}
}
func tokenLookupAssertStmt(pos lexer.Pos, lookup ast.Expr, args []ast.Expr, expected ast.Expr) ast.Stmt {
	return &ast.ExprStmt{
		Position: pos,
		Expr: &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: "assert_eq"},
			Args: []ast.Expr{
				&ast.CallExpr{
					Position: pos,
					Func:     lookup,
					Args:     args,
				},
				expected,
			},
		},
	}
}
func abortPanicBody(pos lexer.Pos, body []ast.Stmt) []ast.Stmt {
	if len(body) == 0 {
		return nil
	}
	return []ast.Stmt{&ast.CanStmt{
		Position:    pos,
		Permissions: []ast.PermissionRef{abortPanicPermission(pos)},
		Body:        body,
	}}
}
func abortPanicPermission(pos lexer.Pos) ast.PermissionRef {
	return ast.PermissionRef{
		Position: pos,
		Name:     "Abort",
		Member:   "Panic",
	}
}
func grammarTokenLookupCond(pos lexer.Pos, compareFunc string, literal string) ast.Expr {
	textExpr := &ast.Ident{Position: pos, Name: "text"}
	literalExpr := &ast.StringLit{Position: pos, Value: literal}
	if compareFunc != "" {
		return &ast.CallExpr{
			Position: pos,
			Func:     &ast.Ident{Position: pos, Name: compareFunc},
			Args:     []ast.Expr{textExpr, literalExpr},
		}
	}
	return &ast.BinaryExpr{
		Position: pos,
		Op:       lexer.TOKEN_EQEQ,
		Left:     textExpr,
		Right:    literalExpr,
	}
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
func rewriteGrammarRecoveryPoliciesTokenAliases(policies []ast.GrammarRecoveryDecl, aliases map[string]string) []ast.GrammarRecoveryDecl {
	if len(policies) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarRecoveryDecl, 0, len(policies))
	for _, policy := range policies {
		rewritten = append(rewritten, ast.GrammarRecoveryDecl{
			Position: policy.Position,
			Name:     policy.Name,
			Message:  policy.Message,
			Until:    rewriteGrammarTermListTokenAliases(policy.Until, aliases),
			Fallback: policy.Fallback,
		})
	}
	return rewritten
}
func rewriteGrammarInfixTablesTokenAliases(tables []ast.GrammarInfixTableDecl, aliases map[string]string) []ast.GrammarInfixTableDecl {
	if len(tables) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarInfixTableDecl, 0, len(tables))
	for _, table := range tables {
		rewritten = append(rewritten, ast.GrammarInfixTableDecl{
			Position: table.Position,
			Name:     table.Name,
			Result:   table.Result,
			Levels:   rewriteGrammarPrecedenceLevelsTokenAliases(table.Levels, aliases),
		})
	}
	return rewritten
}
