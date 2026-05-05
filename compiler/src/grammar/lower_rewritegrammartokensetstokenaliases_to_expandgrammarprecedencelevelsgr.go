package grammar

import "elisacore/src/ast"

func rewriteGrammarTokenSetsTokenAliases(tokenSets []ast.GrammarTokenSetDecl, aliases map[string]string) []ast.GrammarTokenSetDecl {
	if len(tokenSets) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarTokenSetDecl, 0, len(tokenSets))
	for _, tokenSet := range tokenSets {
		rewritten = append(rewritten, ast.GrammarTokenSetDecl{
			Position:    tokenSet.Position,
			Name:        tokenSet.Name,
			TokenFamily: tokenSet.TokenFamily,
			Terms:       rewriteGrammarTermListTokenAliases(tokenSet.Terms, aliases),
		})
	}
	return rewritten
}
func grammarFnMap(grammarFns []ast.GrammarFnDecl) map[string]ast.GrammarFnDecl {
	if len(grammarFns) == 0 {
		return nil
	}
	resolved := make(map[string]ast.GrammarFnDecl, len(grammarFns))
	for _, grammarFn := range grammarFns {
		if grammarFn.Name == "" {
			continue
		}
		resolved[grammarFn.Name] = grammarFn
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}
func addParameterizedGrammarAliasesToGrammarFnMap(grammarFns map[string]ast.GrammarFnDecl, aliases []ast.GrammarAliasDecl) map[string]ast.GrammarFnDecl {
	if len(aliases) == 0 {
		return grammarFns
	}
	resolved := grammarFns
	for _, alias := range aliases {
		if alias.Name == "" || len(alias.Params) == 0 {
			continue
		}
		if resolved == nil {
			resolved = make(map[string]ast.GrammarFnDecl)
		}
		resolved[alias.Name] = ast.GrammarFnDecl{Position: alias.Position, Name: alias.Name, Params: alias.Params, Terms: []ast.GrammarTerm{alias.Term}}
	}
	return resolved
}
func grammarAliasMap(aliases []ast.GrammarAliasDecl) map[string]ast.GrammarAliasDecl {
	if len(aliases) == 0 {
		return nil
	}
	resolved := make(map[string]ast.GrammarAliasDecl, len(aliases))
	for _, alias := range aliases {
		if alias.Name == "" {
			continue
		}
		resolved[alias.Name] = alias
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}
func expandGrammarAliasesGrammarAliases(aliases []ast.GrammarAliasDecl, aliasMap map[string]ast.GrammarAliasDecl) []ast.GrammarAliasDecl {
	if len(aliases) == 0 || len(aliasMap) == 0 {
		return aliases
	}
	expanded := make([]ast.GrammarAliasDecl, 0, len(aliases))
	for _, alias := range aliases {
		seen := map[string]bool{alias.Name: true}
		for _, param := range alias.Params {
			seen[param.Name] = true
		}
		if len(alias.Params) != 0 {
			params := make([]ast.GrammarFnParam, 0, len(alias.Params))
			for _, param := range alias.Params {
				if param.Default != nil {
					param.Default = expandGrammarTermGrammarAliases(param.Default, aliasMap, seen)
				}
				params = append(params, param)
			}
			alias.Params = params
		}
		alias.Term = expandGrammarTermGrammarAliases(alias.Term, aliasMap, seen)
		expanded = append(expanded, alias)
	}
	return expanded
}
func expandGrammarFnsGrammarAliases(grammarFns []ast.GrammarFnDecl, aliases map[string]ast.GrammarAliasDecl) []ast.GrammarFnDecl {
	if len(grammarFns) == 0 || len(aliases) == 0 {
		return grammarFns
	}
	expanded := make([]ast.GrammarFnDecl, 0, len(grammarFns))
	for _, grammarFn := range grammarFns {
		params := make([]ast.GrammarFnParam, 0, len(grammarFn.Params))
		for _, param := range grammarFn.Params {
			if param.Default != nil {
				param.Default = expandGrammarTermGrammarAliases(param.Default, aliases, nil)
			}
			params = append(params, param)
		}
		grammarFn.Params = params
		grammarFn.Terms = expandGrammarTermListGrammarAliases(grammarFn.Terms, aliases, nil)
		expanded = append(expanded, grammarFn)
	}
	return expanded
}
func expandGrammarProductionGrammarAliases(production ast.GrammarProductionDecl, aliases map[string]ast.GrammarAliasDecl) ast.GrammarProductionDecl {
	if len(aliases) == 0 {
		return production
	}
	production.RecoverUntil = expandGrammarTermListGrammarAliases(production.RecoverUntil, aliases, nil)
	terms := make([]ast.GrammarTerm, 0, len(production.Terms))
	for _, term := range production.Terms {
		terms = append(terms, expandGrammarTermGrammarAliases(term, aliases, nil))
	}
	production.Terms = terms
	return production
}
func expandGrammarInfixTablesGrammarAliases(tables []ast.GrammarInfixTableDecl, aliases map[string]ast.GrammarAliasDecl) []ast.GrammarInfixTableDecl {
	if len(tables) == 0 || len(aliases) == 0 {
		return tables
	}
	expanded := make([]ast.GrammarInfixTableDecl, 0, len(tables))
	for _, table := range tables {
		table.Levels = expandGrammarPrecedenceLevelsGrammarAliases(table.Levels, aliases, nil)
		expanded = append(expanded, table)
	}
	return expanded
}
func expandGrammarTermGrammarAliases(term ast.GrammarTerm, aliases map[string]ast.GrammarAliasDecl, seen map[string]bool) ast.GrammarTerm {
	if term == nil || len(aliases) == 0 {
		return term
	}
	switch n := term.(type) {
	case *ast.GrammarCallTerm:
		if !n.Explicit && len(n.Args) == 0 {
			if alias, ok := aliases[n.Name]; ok && len(alias.Params) == 0 && !seen[n.Name] {
				nextSeen := copyGrammarAliasSeen(seen)
				nextSeen[n.Name] = true
				return expandGrammarTermGrammarAliases(alias.Term, aliases, nextSeen)
			}
		}
		return term
	case *ast.GrammarApplyTerm:
		args := make([]ast.GrammarApplyArg, 0, len(n.Args))
		for _, arg := range n.Args {
			arg.Term = expandGrammarTermGrammarAliases(arg.Term, aliases, seen)
			args = append(args, arg)
		}
		return &ast.GrammarApplyTerm{Position: n.Position, Name: n.Name, Direct: n.Direct, Piped: n.Piped, Args: args}
	case *ast.GrammarBindTerm:
		return &ast.GrammarBindTerm{Position: n.Position, Name: n.Name, Term: expandGrammarTermGrammarAliases(n.Term, aliases, seen)}
	case *ast.GrammarAssignTerm:
		return &ast.GrammarAssignTerm{Position: n.Position, Name: n.Name, Term: expandGrammarTermGrammarAliases(n.Term, aliases, seen)}
	case *ast.GrammarReturnTerm:
		return &ast.GrammarReturnTerm{Position: n.Position, Term: expandGrammarTermGrammarAliases(n.Term, aliases, seen)}
	case *ast.GrammarChoiceTerm:
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: expandGrammarTermListGrammarAliases(n.Options, aliases, seen)}
	case *ast.GrammarOptionalTerm:
		return &ast.GrammarOptionalTerm{Position: n.Position, Term: expandGrammarTermGrammarAliases(n.Term, aliases, seen)}
	case *ast.GrammarWhenTerm:
		return &ast.GrammarWhenTerm{Position: n.Position, Cond: n.Cond, TokenKindGate: n.TokenKindGate, Then: expandGrammarTermGrammarAliases(n.Then, aliases, seen), Else: expandGrammarTermGrammarAliases(n.Else, aliases, seen)}
	case *ast.GrammarRecoverTerm:
		return &ast.GrammarRecoverTerm{Position: n.Position, Term: expandGrammarTermGrammarAliases(n.Term, aliases, seen), RecoverPolicy: n.RecoverPolicy, RecoverMsg: n.RecoverMsg, RecoverUntil: expandGrammarTermListGrammarAliases(n.RecoverUntil, aliases, seen), RecoverValue: n.RecoverValue}
	case *ast.GrammarRequiredTerm:
		return &ast.GrammarRequiredTerm{Position: n.Position, Term: expandGrammarTermGrammarAliases(n.Term, aliases, seen), Message: n.Message}
	case *ast.GrammarDelimitedTerm:
		return &ast.GrammarDelimitedTerm{Position: n.Position, Open: expandGrammarTermGrammarAliases(n.Open, aliases, seen), Body: expandGrammarTermGrammarAliases(n.Body, aliases, seen), Close: expandGrammarTermGrammarAliases(n.Close, aliases, seen), Message: n.Message}
	case *ast.GrammarSeqTerm:
		return &ast.GrammarSeqTerm{Position: n.Position, Terms: expandGrammarTermListGrammarAliases(n.Terms, aliases, seen)}
	case *ast.GrammarLookaheadTerm:
		return &ast.GrammarLookaheadTerm{Position: n.Position, Term: expandGrammarTermGrammarAliases(n.Term, aliases, seen)}
	case *ast.GrammarConcatTerm:
		return &ast.GrammarConcatTerm{Position: n.Position, Terms: expandGrammarTermListGrammarAliases(n.Terms, aliases, seen)}
	case *ast.GrammarListTerm:
		return &ast.GrammarListTerm{Position: n.Position, Elem: expandGrammarTermGrammarAliases(n.Elem, aliases, seen), Separator: expandGrammarTermGrammarAliases(n.Separator, aliases, seen), Until: expandGrammarTermListGrammarAliases(n.Until, aliases, seen)}
	case *ast.GrammarRepeatTerm:
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: expandGrammarTermGrammarAliases(n.Elem, aliases, seen), Until: expandGrammarTermListGrammarAliases(n.Until, aliases, seen)}
	case *ast.GrammarFlatRepeatTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: expandGrammarTermGrammarAliases(n.Elem, aliases, seen), Until: expandGrammarTermListGrammarAliases(n.Until, aliases, seen)}
	case *ast.GrammarWhileTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: expandGrammarTermGrammarAliases(n.Elem, aliases, seen), Until: expandGrammarTermListGrammarAliases(n.Until, aliases, seen)}
	case *ast.GrammarSeparatedTerm:
		return &ast.GrammarSeparatedTerm{Position: n.Position, Elem: expandGrammarTermGrammarAliases(n.Elem, aliases, seen), Separator: expandGrammarTermGrammarAliases(n.Separator, aliases, seen), Until: expandGrammarTermListGrammarAliases(n.Until, aliases, seen)}
	case *ast.GrammarSuffixTerm:
		return &ast.GrammarSuffixTerm{Position: n.Position, LeftName: n.LeftName, Seed: expandGrammarTermGrammarAliases(n.Seed, aliases, seen), Arms: expandGrammarPostfixArmsGrammarAliases(n.Arms, aliases, seen)}
	case *ast.GrammarPostfixTerm:
		return &ast.GrammarPostfixTerm{Position: n.Position, LeftName: n.LeftName, Seed: expandGrammarTermGrammarAliases(n.Seed, aliases, seen), Arms: expandGrammarPostfixArmsGrammarAliases(n.Arms, aliases, seen)}
	case *ast.GrammarPrecedenceTerm:
		return &ast.GrammarPrecedenceTerm{Position: n.Position, Assoc: n.Assoc, Result: n.Result, Levels: expandGrammarPrecedenceLevelsGrammarAliases(n.Levels, aliases, seen), LeftName: n.LeftName, Seed: expandGrammarTermGrammarAliases(n.Seed, aliases, seen), Arms: expandGrammarPrecedenceArmsGrammarAliases(n.Arms, aliases, seen)}
	default:
		return term
	}
}
func copyGrammarAliasSeen(seen map[string]bool) map[string]bool {
	copied := make(map[string]bool, len(seen)+1)
	for name, ok := range seen {
		copied[name] = ok
	}
	return copied
}
func expandGrammarTermListGrammarAliases(terms []ast.GrammarTerm, aliases map[string]ast.GrammarAliasDecl, seen map[string]bool) []ast.GrammarTerm {
	if len(terms) == 0 {
		return nil
	}
	expanded := make([]ast.GrammarTerm, 0, len(terms))
	for _, term := range terms {
		expanded = append(expanded, expandGrammarTermGrammarAliases(term, aliases, seen))
	}
	return expanded
}
func expandGrammarPostfixArmsGrammarAliases(arms []ast.GrammarPostfixArm, aliases map[string]ast.GrammarAliasDecl, seen map[string]bool) []ast.GrammarPostfixArm {
	if len(arms) == 0 {
		return nil
	}
	expanded := make([]ast.GrammarPostfixArm, 0, len(arms))
	for _, arm := range arms {
		expanded = append(expanded, ast.GrammarPostfixArm{Position: arm.Position, OpName: arm.OpName, Op: expandGrammarTermGrammarAliases(arm.Op, aliases, seen), Block: arm.Block, Bindings: expandGrammarBindingsGrammarAliases(arm.Bindings, aliases, seen), Value: arm.Value})
	}
	return expanded
}
func expandGrammarBindingsGrammarAliases(bindingsList []*ast.GrammarBindTerm, aliases map[string]ast.GrammarAliasDecl, seen map[string]bool) []*ast.GrammarBindTerm {
	if len(bindingsList) == 0 {
		return nil
	}
	expanded := make([]*ast.GrammarBindTerm, 0, len(bindingsList))
	for _, binding := range bindingsList {
		if binding == nil {
			continue
		}
		expanded = append(expanded, &ast.GrammarBindTerm{Position: binding.Position, Name: binding.Name, Term: expandGrammarTermGrammarAliases(binding.Term, aliases, seen)})
	}
	return expanded
}
func expandGrammarPrecedenceArmsGrammarAliases(arms []ast.GrammarPrecedenceArm, aliases map[string]ast.GrammarAliasDecl, seen map[string]bool) []ast.GrammarPrecedenceArm {
	if len(arms) == 0 {
		return nil
	}
	expanded := make([]ast.GrammarPrecedenceArm, 0, len(arms))
	for _, arm := range arms {
		expanded = append(expanded, ast.GrammarPrecedenceArm{Position: arm.Position, OpName: arm.OpName, Op: expandGrammarTermGrammarAliases(arm.Op, aliases, seen), Block: arm.Block, Bindings: expandGrammarBindingsGrammarAliases(arm.Bindings, aliases, seen), Value: arm.Value})
	}
	return expanded
}
func expandGrammarPrecedenceLevelsGrammarAliases(levels []ast.GrammarPrecedenceLevel, aliases map[string]ast.GrammarAliasDecl, seen map[string]bool) []ast.GrammarPrecedenceLevel {
	if len(levels) == 0 {
		return nil
	}
	expanded := make([]ast.GrammarPrecedenceLevel, 0, len(levels))
	for _, level := range levels {
		expanded = append(expanded, ast.GrammarPrecedenceLevel{Position: level.Position, Assoc: level.Assoc, Name: level.Name, LeftName: level.LeftName, Seed: expandGrammarTermGrammarAliases(level.Seed, aliases, seen), Arms: expandGrammarPrecedenceArmsGrammarAliases(level.Arms, aliases, seen)})
	}
	return expanded
}
func expandGrammarProductionGrammarFns(production ast.GrammarProductionDecl, grammarFns map[string]ast.GrammarFnDecl) ast.GrammarProductionDecl {
	if len(grammarFns) == 0 {
		return production
	}
	production.RecoverUntil = expandGrammarTermListGrammarFns(production.RecoverUntil, grammarFns, nil)
	terms := make([]ast.GrammarTerm, 0, len(production.Terms))
	for _, term := range production.Terms {
		terms = append(terms, expandGrammarTermGrammarFns(term, grammarFns, nil))
	}
	production.Terms = terms
	return production
}
func expandGrammarInfixTablesGrammarFns(tables []ast.GrammarInfixTableDecl, grammarFns map[string]ast.GrammarFnDecl) []ast.GrammarInfixTableDecl {
	if len(tables) == 0 || len(grammarFns) == 0 {
		return tables
	}
	expanded := make([]ast.GrammarInfixTableDecl, 0, len(tables))
	for _, table := range tables {
		table.Levels = expandGrammarPrecedenceLevelsGrammarFns(table.Levels, grammarFns, nil)
		expanded = append(expanded, table)
	}
	return expanded
}

type grammarFnBindings struct {
	terms map[string]ast.GrammarTerm
	exprs map[string]ast.Expr
}

func expandGrammarTermGrammarFns(term ast.GrammarTerm, grammarFns map[string]ast.GrammarFnDecl, bindings *grammarFnBindings) ast.GrammarTerm {
	if term == nil {
		return nil
	}
	if bindings != nil {
		switch n := term.(type) {
		case *ast.GrammarCallTerm:
			if !n.Explicit && len(n.Args) == 0 {
				if replacement, ok := bindings.terms[n.Name]; ok {
					return replacement
				}
				if replacement, ok := bindings.exprs[n.Name]; ok {
					return &ast.GrammarExprTerm{Position: n.Position, Expr: replacement}
				}
			} else if len(n.Args) > 0 {
				if _, isFn := grammarFns[n.Name]; isFn {
					// Convert expression args that reference grammar parameter bindings into
					// grammar apply args so the callee grammar function can expand them properly.
					applyArgs := make([]ast.GrammarApplyArg, 0, len(n.Args))
					for _, arg := range n.Args {
						var argTerm ast.GrammarTerm
						if ident, ok := arg.(*ast.Ident); ok {
							if t, ok2 := bindings.terms[ident.Name]; ok2 {
								argTerm = t
							} else if e, ok2 := bindings.exprs[ident.Name]; ok2 {
								argTerm = &ast.GrammarExprTerm{Position: ident.Position, Expr: e}
							}
						}
						if argTerm == nil {
							argTerm = &ast.GrammarExprTerm{Position: n.Position, Expr: arg}
						}
						applyArgs = append(applyArgs, ast.GrammarApplyArg{Position: n.Position, Term: argTerm})
					}
					apply := &ast.GrammarApplyTerm{Position: n.Position, Name: n.Name, Args: applyArgs}
					expanded := expandGrammarApplyTerm(apply, grammarFns)
					if expanded == apply {
						return apply
					}
					return expandGrammarTermGrammarFns(expanded, grammarFns, bindings)
				}
			}
		case *ast.GrammarTokenSetRefTerm:
			if replacement, ok := bindings.terms[n.Name]; ok {
				return replacement
			}
			if replacement, ok := bindings.exprs[n.Name]; ok {
				return &ast.GrammarExprTerm{Position: n.Position, Expr: replacement}
			}
		case *ast.GrammarFirstTerm:
			if replacement, ok := bindings.terms[n.Name]; ok {
				return replacement
			}
		}
	}
	switch n := term.(type) {
	case *ast.GrammarApplyTerm:
		apply := n
		if bindings != nil && len(n.Args) != 0 {
			args := make([]ast.GrammarApplyArg, 0, len(n.Args))
			for _, arg := range n.Args {
				arg.Term = expandGrammarTermGrammarFns(arg.Term, grammarFns, bindings)
				args = append(args, arg)
			}
			apply = &ast.GrammarApplyTerm{Position: n.Position, Name: n.Name, Direct: n.Direct, Piped: n.Piped, Args: args}
		}
		expanded := expandGrammarApplyTerm(apply, grammarFns)
		if expanded == apply {
			return apply
		}
		return expandGrammarTermGrammarFns(expanded, grammarFns, bindings)
	case *ast.GrammarBindTerm:
		return &ast.GrammarBindTerm{Position: n.Position, Name: n.Name, Term: expandGrammarTermGrammarFns(n.Term, grammarFns, bindings)}
	case *ast.GrammarAssignTerm:
		return &ast.GrammarAssignTerm{Position: n.Position, Name: n.Name, Term: expandGrammarTermGrammarFns(n.Term, grammarFns, bindings)}
	case *ast.GrammarReturnTerm:
		return &ast.GrammarReturnTerm{Position: n.Position, Term: expandGrammarTermGrammarFns(n.Term, grammarFns, bindings)}
	case *ast.GrammarChoiceTerm:
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: expandGrammarTermListGrammarFns(n.Options, grammarFns, bindings)}
	case *ast.GrammarOptionalTerm:
		return &ast.GrammarOptionalTerm{Position: n.Position, Term: expandGrammarTermGrammarFns(n.Term, grammarFns, bindings)}
	case *ast.GrammarWhenTerm:
		return &ast.GrammarWhenTerm{Position: n.Position, Cond: expandGrammarExprGrammarFns(n.Cond, bindings), TokenKindGate: n.TokenKindGate, Then: expandGrammarTermGrammarFns(n.Then, grammarFns, bindings), Else: expandGrammarTermGrammarFns(n.Else, grammarFns, bindings)}
	case *ast.GrammarRecoverTerm:
		return &ast.GrammarRecoverTerm{Position: n.Position, Term: expandGrammarTermGrammarFns(n.Term, grammarFns, bindings), RecoverPolicy: n.RecoverPolicy, RecoverMsg: expandGrammarExprGrammarFns(n.RecoverMsg, bindings), RecoverUntil: expandGrammarTermListGrammarFns(n.RecoverUntil, grammarFns, bindings), RecoverValue: expandGrammarExprGrammarFns(n.RecoverValue, bindings)}
	case *ast.GrammarRequiredTerm:
		return &ast.GrammarRequiredTerm{Position: n.Position, Term: expandGrammarTermGrammarFns(n.Term, grammarFns, bindings), Message: expandGrammarExprGrammarFns(n.Message, bindings)}
	case *ast.GrammarDelimitedTerm:
		return &ast.GrammarDelimitedTerm{Position: n.Position, Open: expandGrammarTermGrammarFns(n.Open, grammarFns, bindings), Body: expandGrammarTermGrammarFns(n.Body, grammarFns, bindings), Close: expandGrammarTermGrammarFns(n.Close, grammarFns, bindings), Message: expandGrammarExprGrammarFns(n.Message, bindings)}
	case *ast.GrammarSeqTerm:
		return &ast.GrammarSeqTerm{Position: n.Position, Terms: expandGrammarTermListGrammarFns(n.Terms, grammarFns, bindings)}
	case *ast.GrammarLookaheadTerm:
		return &ast.GrammarLookaheadTerm{Position: n.Position, Term: expandGrammarTermGrammarFns(n.Term, grammarFns, bindings)}
	case *ast.GrammarFirstTerm:
		return &ast.GrammarFirstTerm{Position: n.Position, Name: n.Name}
	case *ast.GrammarSingletonTerm:
		return &ast.GrammarSingletonTerm{Position: n.Position, Type: n.Type, Value: expandGrammarExprGrammarFns(n.Value, bindings)}
	case *ast.GrammarEmptyTerm:
		return &ast.GrammarEmptyTerm{Position: n.Position, Type: n.Type}
	case *ast.GrammarConcatTerm:
		return &ast.GrammarConcatTerm{Position: n.Position, Terms: expandGrammarTermListGrammarFns(n.Terms, grammarFns, bindings)}
	case *ast.GrammarListTerm:
		return &ast.GrammarListTerm{Position: n.Position, Elem: expandGrammarTermGrammarFns(n.Elem, grammarFns, bindings), Separator: expandGrammarTermGrammarFns(n.Separator, grammarFns, bindings), Until: expandGrammarTermListGrammarFns(n.Until, grammarFns, bindings)}
	case *ast.GrammarRepeatTerm:
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: expandGrammarTermGrammarFns(n.Elem, grammarFns, bindings), Until: expandGrammarTermListGrammarFns(n.Until, grammarFns, bindings)}
	case *ast.GrammarFlatRepeatTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: expandGrammarTermGrammarFns(n.Elem, grammarFns, bindings), Until: expandGrammarTermListGrammarFns(n.Until, grammarFns, bindings)}
	case *ast.GrammarWhileTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: expandGrammarTermGrammarFns(n.Elem, grammarFns, bindings), Until: expandGrammarTermListGrammarFns(n.Until, grammarFns, bindings)}
	case *ast.GrammarSeparatedTerm:
		return &ast.GrammarSeparatedTerm{Position: n.Position, Elem: expandGrammarTermGrammarFns(n.Elem, grammarFns, bindings), Separator: expandGrammarTermGrammarFns(n.Separator, grammarFns, bindings), Until: expandGrammarTermListGrammarFns(n.Until, grammarFns, bindings)}
	case *ast.GrammarSuffixTerm:
		return &ast.GrammarSuffixTerm{Position: n.Position, LeftName: n.LeftName, Seed: expandGrammarTermGrammarFns(n.Seed, grammarFns, bindings), Arms: expandGrammarPostfixArmsGrammarFns(n.Arms, grammarFns, bindings)}
	case *ast.GrammarPostfixTerm:
		return &ast.GrammarPostfixTerm{Position: n.Position, LeftName: n.LeftName, Seed: expandGrammarTermGrammarFns(n.Seed, grammarFns, bindings), Arms: expandGrammarPostfixArmsGrammarFns(n.Arms, grammarFns, bindings)}
	case *ast.GrammarPrecedenceTerm:
		return &ast.GrammarPrecedenceTerm{Position: n.Position, Assoc: n.Assoc, Result: n.Result, Levels: expandGrammarPrecedenceLevelsGrammarFns(n.Levels, grammarFns, bindings), LeftName: n.LeftName, Seed: expandGrammarTermGrammarFns(n.Seed, grammarFns, bindings), Arms: expandGrammarPrecedenceArmsGrammarFns(n.Arms, grammarFns, bindings)}
	default:
		return term
	}
}
func expandGrammarApplyTerm(term *ast.GrammarApplyTerm, grammarFns map[string]ast.GrammarFnDecl) ast.GrammarTerm {
	grammarFn, ok := grammarFns[term.Name]
	if !ok || len(grammarFn.Terms) == 0 {
		return term
	}
	resolved, ok := resolveGrammarApplyArgs(term, grammarFn)
	if !ok {
		return term
	}
	bindings := &grammarFnBindings{terms: make(map[string]ast.GrammarTerm, len(grammarFn.Params)), exprs: make(map[string]ast.Expr, len(grammarFn.Params))}
	for index, param := range grammarFn.Params {
		if resolved[index].Term != nil {
			bindings.terms[param.Name] = resolved[index].Term
		}
		if resolved[index].Expr != nil {
			bindings.exprs[param.Name] = resolved[index].Expr
		}
	}
	terms := expandGrammarTermListGrammarFns(grammarFn.Terms, grammarFns, bindings)
	if len(terms) == 1 {
		return terms[0]
	}
	return &ast.GrammarSeqTerm{Position: grammarFn.Position, Terms: terms}
}

type resolvedGrammarApplyArg struct {
	Term ast.GrammarTerm
	Expr ast.Expr
}

func resolveGrammarApplyArgs(term *ast.GrammarApplyTerm, grammarFn ast.GrammarFnDecl) ([]resolvedGrammarApplyArg, bool) {
	return resolveGrammarApplyArgsForParams(term.Args, grammarFn.Params)
}
func resolveGrammarApplyArgsForParams(args []ast.GrammarApplyArg, params []ast.GrammarFnParam) ([]resolvedGrammarApplyArg, bool) {
	resolved := make([]resolvedGrammarApplyArg, len(params))
	filled := make([]bool, len(params))
	paramIndex := make(map[string]int, len(params))
	for index, param := range params {
		paramIndex[param.Name] = index
	}

	nextPositional := 0
	seenNamed := false
	for _, arg := range args {
		if arg.Name != "" {
			seenNamed = true
			index, found := paramIndex[arg.Name]
			if !found || filled[index] {
				return nil, false
			}
			resolved[index] = resolvedGrammarApplyArg{Term: arg.Term, Expr: grammarFnExprArg(arg.Term)}
			filled[index] = true
			continue
		}
		if seenNamed {
			return nil, false
		}
		for nextPositional < len(filled) && filled[nextPositional] {
			nextPositional++
		}
		if nextPositional >= len(params) {
			return nil, false
		}
		resolved[nextPositional] = resolvedGrammarApplyArg{Term: arg.Term, Expr: grammarFnExprArg(arg.Term)}
		filled[nextPositional] = true
		nextPositional++
	}

	for index, param := range params {
		if filled[index] {
			continue
		}
		if param.Default != nil {
			resolved[index] = resolvedGrammarApplyArg{Term: param.Default, Expr: grammarFnExprArg(param.Default)}
			filled[index] = true
			continue
		}
		if param.DefaultExpr != nil {
			resolved[index] = resolvedGrammarApplyArg{Expr: param.DefaultExpr}
			filled[index] = true
			continue
		}
		return nil, false
	}
	return resolved, true
}
func grammarFnExprArg(term ast.GrammarTerm) ast.Expr {
	if exprTerm, ok := term.(*ast.GrammarExprTerm); ok {
		return exprTerm.Expr
	}
	return nil
}
func expandGrammarExprGrammarFns(expr ast.Expr, bindings *grammarFnBindings) ast.Expr {
	if expr == nil || bindings == nil {
		return expr
	}
	if ident, ok := expr.(*ast.Ident); ok {
		if replacement, ok := bindings.exprs[ident.Name]; ok {
			return replacement
		}
	}
	return expr
}
func expandGrammarTermListGrammarFns(terms []ast.GrammarTerm, grammarFns map[string]ast.GrammarFnDecl, bindings *grammarFnBindings) []ast.GrammarTerm {
	if len(terms) == 0 {
		return nil
	}
	expanded := make([]ast.GrammarTerm, 0, len(terms))
	for _, term := range terms {
		expanded = append(expanded, expandGrammarTermGrammarFns(term, grammarFns, bindings))
	}
	return expanded
}
func expandGrammarPostfixArmsGrammarFns(arms []ast.GrammarPostfixArm, grammarFns map[string]ast.GrammarFnDecl, bindings *grammarFnBindings) []ast.GrammarPostfixArm {
	if len(arms) == 0 {
		return nil
	}
	expanded := make([]ast.GrammarPostfixArm, 0, len(arms))
	for _, arm := range arms {
		expanded = append(expanded, ast.GrammarPostfixArm{Position: arm.Position, OpName: arm.OpName, Op: expandGrammarTermGrammarFns(arm.Op, grammarFns, bindings), Block: arm.Block, Bindings: expandGrammarBindingsGrammarFns(arm.Bindings, grammarFns, bindings), Value: expandGrammarExprGrammarFns(arm.Value, bindings)})
	}
	return expanded
}
func expandGrammarBindingsGrammarFns(bindingsList []*ast.GrammarBindTerm, grammarFns map[string]ast.GrammarFnDecl, bindings *grammarFnBindings) []*ast.GrammarBindTerm {
	if len(bindingsList) == 0 {
		return nil
	}
	expanded := make([]*ast.GrammarBindTerm, 0, len(bindingsList))
	for _, binding := range bindingsList {
		if binding == nil {
			continue
		}
		expanded = append(expanded, &ast.GrammarBindTerm{Position: binding.Position, Name: binding.Name, Term: expandGrammarTermGrammarFns(binding.Term, grammarFns, bindings)})
	}
	return expanded
}
func expandGrammarPrecedenceArmsGrammarFns(arms []ast.GrammarPrecedenceArm, grammarFns map[string]ast.GrammarFnDecl, bindings *grammarFnBindings) []ast.GrammarPrecedenceArm {
	if len(arms) == 0 {
		return nil
	}
	expanded := make([]ast.GrammarPrecedenceArm, 0, len(arms))
	for _, arm := range arms {
		expanded = append(expanded, ast.GrammarPrecedenceArm{Position: arm.Position, OpName: arm.OpName, Op: expandGrammarTermGrammarFns(arm.Op, grammarFns, bindings), Block: arm.Block, Bindings: expandGrammarBindingsGrammarFns(arm.Bindings, grammarFns, bindings), Value: expandGrammarExprGrammarFns(arm.Value, bindings)})
	}
	return expanded
}
func expandGrammarPrecedenceLevelsGrammarFns(levels []ast.GrammarPrecedenceLevel, grammarFns map[string]ast.GrammarFnDecl, bindings *grammarFnBindings) []ast.GrammarPrecedenceLevel {
	if len(levels) == 0 {
		return nil
	}
	expanded := make([]ast.GrammarPrecedenceLevel, 0, len(levels))
	for _, level := range levels {
		expanded = append(expanded, ast.GrammarPrecedenceLevel{Position: level.Position, Assoc: level.Assoc, Name: level.Name, LeftName: level.LeftName, Seed: expandGrammarTermGrammarFns(level.Seed, grammarFns, bindings), Arms: expandGrammarPrecedenceArmsGrammarFns(level.Arms, grammarFns, bindings)})
	}
	return expanded
}
