package grammar

import "elisacore/src/ast"

func resolveGrammarProductionFirstSets(production ast.GrammarProductionDecl, productions map[string]resolvedGrammarProduction) ast.GrammarProductionDecl {
	production.RecoverUntil = resolveGrammarFirstRefsInStopList(production.RecoverUntil, productions)
	if len(production.Terms) == 0 {
		return production
	}
	terms := make([]ast.GrammarTerm, 0, len(production.Terms))
	for _, term := range production.Terms {
		terms = append(terms, resolveGrammarTermFirstSets(term, productions))
	}
	production.Terms = terms
	return production
}

func resolveGrammarRecoveryPoliciesFirstSets(policies []ast.GrammarRecoveryDecl, productions map[string]resolvedGrammarProduction) []ast.GrammarRecoveryDecl {
	if len(policies) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarRecoveryDecl, 0, len(policies))
	for _, policy := range policies {
		resolved = append(resolved, ast.GrammarRecoveryDecl{Position: policy.Position, Name: policy.Name, Message: policy.Message, Until: resolveGrammarFirstRefsInStopList(policy.Until, productions), Fallback: policy.Fallback})
	}
	return resolved
}

func resolveGrammarTokenSetsFirstSets(tokenSets []ast.GrammarTokenSetDecl, productions map[string]resolvedGrammarProduction) []ast.GrammarTokenSetDecl {
	if len(tokenSets) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarTokenSetDecl, 0, len(tokenSets))
	for _, tokenSet := range tokenSets {
		resolved = append(resolved, ast.GrammarTokenSetDecl{Position: tokenSet.Position, Name: tokenSet.Name, TokenFamily: tokenSet.TokenFamily, Terms: resolveGrammarFirstRefsInStopList(tokenSet.Terms, productions)})
	}
	return resolved
}

func resolveGrammarFirstRefsInStopList(stops []ast.GrammarTerm, productions map[string]resolvedGrammarProduction) []ast.GrammarTerm {
	if len(stops) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarTerm, 0, len(stops))
	for _, stop := range stops {
		resolved = append(resolved, resolveGrammarFirstStop(stop, productions, nil)...)
	}
	return resolved
}

func resolveGrammarTermFirstSets(term ast.GrammarTerm, productions map[string]resolvedGrammarProduction) ast.GrammarTerm {
	if term == nil {
		return nil
	}
	switch n := term.(type) {
	case *ast.GrammarFirstTerm:
		terms := resolveGrammarFirstStop(n, productions, nil)
		if len(terms) == 1 && terms[0] == n {
			return term
		}
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: terms}
	case *ast.GrammarBindTerm:
		return &ast.GrammarBindTerm{Position: n.Position, Name: n.Name, Term: resolveGrammarTermFirstSets(n.Term, productions)}
	case *ast.GrammarAssignTerm:
		return &ast.GrammarAssignTerm{Position: n.Position, Name: n.Name, Term: resolveGrammarTermFirstSets(n.Term, productions)}
	case *ast.GrammarReturnTerm:
		return &ast.GrammarReturnTerm{Position: n.Position, Term: resolveGrammarTermFirstSets(n.Term, productions)}
	case *ast.GrammarChoiceTerm:
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: resolveGrammarTermListFirstSets(n.Options, productions)}
	case *ast.GrammarOptionalTerm:
		return &ast.GrammarOptionalTerm{Position: n.Position, Term: resolveGrammarTermFirstSets(n.Term, productions)}
	case *ast.GrammarWhenTerm:
		return &ast.GrammarWhenTerm{Position: n.Position, Cond: n.Cond, TokenKindGate: n.TokenKindGate, Then: resolveGrammarTermFirstSets(n.Then, productions), Else: resolveGrammarTermFirstSets(n.Else, productions)}
	case *ast.GrammarRecoverTerm:
		return &ast.GrammarRecoverTerm{Position: n.Position, Term: resolveGrammarTermFirstSets(n.Term, productions), RecoverPolicy: n.RecoverPolicy, RecoverMsg: n.RecoverMsg, RecoverUntil: resolveGrammarFirstRefsInStopList(n.RecoverUntil, productions), RecoverValue: n.RecoverValue}
	case *ast.GrammarRequiredTerm:
		return &ast.GrammarRequiredTerm{Position: n.Position, Term: resolveGrammarTermFirstSets(n.Term, productions), Message: n.Message}
	case *ast.GrammarDelimitedTerm:
		return &ast.GrammarDelimitedTerm{Position: n.Position, Open: resolveGrammarTermFirstSets(n.Open, productions), Body: resolveGrammarTermFirstSets(n.Body, productions), Close: resolveGrammarTermFirstSets(n.Close, productions), Message: n.Message}
	case *ast.GrammarSeqTerm:
		return &ast.GrammarSeqTerm{Position: n.Position, Terms: resolveGrammarTermListFirstSets(n.Terms, productions)}
	case *ast.GrammarLookaheadTerm:
		return &ast.GrammarLookaheadTerm{Position: n.Position, Term: resolveGrammarTermFirstSets(n.Term, productions)}
	case *ast.GrammarListTerm:
		return &ast.GrammarListTerm{Position: n.Position, Elem: resolveGrammarTermFirstSets(n.Elem, productions), Separator: resolveGrammarTermFirstSets(n.Separator, productions), Until: resolveGrammarFirstRefsInStopList(n.Until, productions)}
	case *ast.GrammarRepeatTerm:
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: resolveGrammarTermFirstSets(n.Elem, productions), Until: resolveGrammarFirstRefsInStopList(n.Until, productions)}
	case *ast.GrammarFlatRepeatTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: resolveGrammarTermFirstSets(n.Elem, productions), Until: resolveGrammarFirstRefsInStopList(n.Until, productions)}
	case *ast.GrammarWhileTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: resolveGrammarTermFirstSets(n.Elem, productions), Until: resolveGrammarFirstRefsInStopList(n.Until, productions)}
	case *ast.GrammarSeparatedTerm:
		return &ast.GrammarSeparatedTerm{Position: n.Position, Elem: resolveGrammarTermFirstSets(n.Elem, productions), Separator: resolveGrammarTermFirstSets(n.Separator, productions), Until: resolveGrammarFirstRefsInStopList(n.Until, productions)}
	case *ast.GrammarSuffixTerm:
		return &ast.GrammarSuffixTerm{Position: n.Position, LeftName: n.LeftName, Seed: resolveGrammarTermFirstSets(n.Seed, productions), Arms: resolveGrammarPostfixArmsFirstSets(n.Arms, productions)}
	case *ast.GrammarPostfixTerm:
		return &ast.GrammarPostfixTerm{Position: n.Position, LeftName: n.LeftName, Seed: resolveGrammarTermFirstSets(n.Seed, productions), Arms: resolveGrammarPostfixArmsFirstSets(n.Arms, productions)}
	case *ast.GrammarPrecedenceTerm:
		return &ast.GrammarPrecedenceTerm{Position: n.Position, Assoc: n.Assoc, Result: n.Result, Levels: resolveGrammarPrecedenceLevelsFirstSets(n.Levels, productions), LeftName: n.LeftName, Seed: resolveGrammarTermFirstSets(n.Seed, productions), Arms: resolveGrammarPrecedenceArmsFirstSets(n.Arms, productions)}
	default:
		return term
	}
}

func resolveGrammarTermListFirstSets(terms []ast.GrammarTerm, productions map[string]resolvedGrammarProduction) []ast.GrammarTerm {
	if len(terms) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarTerm, 0, len(terms))
	for _, term := range terms {
		resolved = append(resolved, resolveGrammarTermFirstSets(term, productions))
	}
	return resolved
}

func resolveGrammarBindingsFirstSets(bindings []*ast.GrammarBindTerm, productions map[string]resolvedGrammarProduction) []*ast.GrammarBindTerm {
	if len(bindings) == 0 {
		return nil
	}
	resolved := make([]*ast.GrammarBindTerm, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		resolved = append(resolved, &ast.GrammarBindTerm{Position: binding.Position, Name: binding.Name, Term: resolveGrammarTermFirstSets(binding.Term, productions)})
	}
	return resolved
}

func resolveGrammarPostfixArmsFirstSets(arms []ast.GrammarPostfixArm, productions map[string]resolvedGrammarProduction) []ast.GrammarPostfixArm {
	if len(arms) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarPostfixArm, 0, len(arms))
	for _, arm := range arms {
		resolved = append(resolved, ast.GrammarPostfixArm{Position: arm.Position, OpName: arm.OpName, Op: resolveGrammarTermFirstSets(arm.Op, productions), Block: arm.Block, Bindings: resolveGrammarBindingsFirstSets(arm.Bindings, productions), Value: arm.Value})
	}
	return resolved
}

func resolveGrammarPrecedenceArmsFirstSets(arms []ast.GrammarPrecedenceArm, productions map[string]resolvedGrammarProduction) []ast.GrammarPrecedenceArm {
	if len(arms) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarPrecedenceArm, 0, len(arms))
	for _, arm := range arms {
		resolved = append(resolved, ast.GrammarPrecedenceArm{Position: arm.Position, OpName: arm.OpName, Op: resolveGrammarTermFirstSets(arm.Op, productions), Block: arm.Block, Bindings: resolveGrammarBindingsFirstSets(arm.Bindings, productions), Value: arm.Value})
	}
	return resolved
}

func resolveGrammarPrecedenceLevelsFirstSets(levels []ast.GrammarPrecedenceLevel, productions map[string]resolvedGrammarProduction) []ast.GrammarPrecedenceLevel {
	if len(levels) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarPrecedenceLevel, 0, len(levels))
	for _, level := range levels {
		resolved = append(resolved, ast.GrammarPrecedenceLevel{Position: level.Position, Assoc: level.Assoc, Name: level.Name, LeftName: level.LeftName, Seed: resolveGrammarTermFirstSets(level.Seed, productions), Arms: resolveGrammarPrecedenceArmsFirstSets(level.Arms, productions)})
	}
	return resolved
}
