package grammar

import "llcontext/src/ast"

func grammarTokenSetMap(tokenSets []ast.GrammarTokenSetDecl) map[string]ast.GrammarTokenSetDecl {
	if len(tokenSets) == 0 {
		return nil
	}
	resolved := make(map[string]ast.GrammarTokenSetDecl, len(tokenSets))
	for _, tokenSet := range tokenSets {
		if tokenSet.Name == "" {
			continue
		}
		resolved[tokenSet.Name] = tokenSet
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

func rewriteGrammarTokenSetBareRefsToKinds(tokenSets []ast.GrammarTokenSetDecl, setMap map[string]ast.GrammarTokenSetDecl) []ast.GrammarTokenSetDecl {
	if len(tokenSets) == 0 {
		return nil
	}
	rewritten := make([]ast.GrammarTokenSetDecl, 0, len(tokenSets))
	for _, tokenSet := range tokenSets {
		terms := make([]ast.GrammarTerm, 0, len(tokenSet.Terms))
		for _, term := range tokenSet.Terms {
			terms = append(terms, rewriteGrammarTokenSetBareRefToKind(term, setMap))
		}
		rewritten = append(rewritten, ast.GrammarTokenSetDecl{Position: tokenSet.Position, Name: tokenSet.Name, TokenFamily: tokenSet.TokenFamily, Terms: terms})
	}
	return rewritten
}

func rewriteGrammarTokenSetBareRefToKind(term ast.GrammarTerm, setMap map[string]ast.GrammarTokenSetDecl) ast.GrammarTerm {
	ref, ok := term.(*ast.GrammarTokenSetRefTerm)
	if !ok {
		return term
	}
	if _, ok := setMap[ref.Name]; ok {
		return term
	}
	return &ast.GrammarTokenKindTerm{Position: ref.Position, Kind: ref.Name}
}

func resolveGrammarProductionTokenSets(production ast.GrammarProductionDecl, tokenSets map[string]ast.GrammarTokenSetDecl) ast.GrammarProductionDecl {
	production.RecoverUntil = resolveGrammarTokenSetRefsInStopList(production.RecoverUntil, tokenSets)
	if len(production.Terms) == 0 {
		return production
	}
	terms := make([]ast.GrammarTerm, 0, len(production.Terms))
	for _, term := range production.Terms {
		terms = append(terms, resolveGrammarTermTokenSets(term, tokenSets))
	}
	production.Terms = terms
	return production
}

func resolveGrammarRecoveryPoliciesTokenSets(policies []ast.GrammarRecoveryDecl, tokenSets map[string]ast.GrammarTokenSetDecl) []ast.GrammarRecoveryDecl {
	if len(policies) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarRecoveryDecl, 0, len(policies))
	for _, policy := range policies {
		resolved = append(resolved, ast.GrammarRecoveryDecl{
			Position: policy.Position,
			Name:     policy.Name,
			Message:  policy.Message,
			Until:    resolveGrammarTokenSetRefsInStopList(policy.Until, tokenSets),
			Fallback: policy.Fallback,
		})
	}
	return resolved
}

func resolveGrammarTokenSetsTokenSets(tokenSets []ast.GrammarTokenSetDecl, setMap map[string]ast.GrammarTokenSetDecl) []ast.GrammarTokenSetDecl {
	if len(tokenSets) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarTokenSetDecl, 0, len(tokenSets))
	for _, tokenSet := range tokenSets {
		resolved = append(resolved, ast.GrammarTokenSetDecl{
			Position:    tokenSet.Position,
			Name:        tokenSet.Name,
			TokenFamily: tokenSet.TokenFamily,
			Terms:       resolveGrammarTokenSetRefsInStopList(tokenSet.Terms, setMap),
		})
	}
	return resolved
}

func resolveGrammarTokenSetRefsInStopList(stops []ast.GrammarTerm, tokenSets map[string]ast.GrammarTokenSetDecl) []ast.GrammarTerm {
	if len(stops) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarTerm, 0, len(stops))
	for _, stop := range stops {
		resolved = append(resolved, resolveGrammarTokenSetStop(stop, tokenSets, nil)...)
	}
	return resolved
}

func resolveGrammarTokenSetStop(stop ast.GrammarTerm, tokenSets map[string]ast.GrammarTokenSetDecl, seen map[string]bool) []ast.GrammarTerm {
	ref, ok := stop.(*ast.GrammarTokenSetRefTerm)
	if !ok || len(tokenSets) == 0 {
		return []ast.GrammarTerm{stop}
	}
	tokenSet, ok := tokenSets[ref.Name]
	if !ok {
		return []ast.GrammarTerm{stop}
	}
	if seen == nil {
		seen = make(map[string]bool)
	}
	if seen[ref.Name] {
		return nil
	}
	seen[ref.Name] = true
	resolved := make([]ast.GrammarTerm, 0, len(tokenSet.Terms))
	for _, term := range tokenSet.Terms {
		resolved = append(resolved, resolveGrammarTokenSetStop(term, tokenSets, seen)...)
	}
	delete(seen, ref.Name)
	return resolved
}

func resolveGrammarTermTokenSets(term ast.GrammarTerm, tokenSets map[string]ast.GrammarTokenSetDecl) ast.GrammarTerm {
	if term == nil {
		return nil
	}
	switch n := term.(type) {
	case *ast.GrammarTokenSetRefTerm:
		terms := resolveGrammarTokenSetStop(n, tokenSets, nil)
		if len(terms) == 1 && terms[0] == n {
			return term
		}
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: terms}
	case *ast.GrammarCallTerm:
		if !n.Explicit && len(n.Args) == 0 {
			ref := &ast.GrammarTokenSetRefTerm{Position: n.Position, Name: n.Name}
			terms := resolveGrammarTokenSetStop(ref, tokenSets, nil)
			if len(terms) != 1 || terms[0] != ref {
				return &ast.GrammarChoiceTerm{Position: n.Position, Options: terms}
			}
		}
		return term
	case *ast.GrammarBindTerm:
		return &ast.GrammarBindTerm{Position: n.Position, Name: n.Name, Term: resolveGrammarTermTokenSets(n.Term, tokenSets)}
	case *ast.GrammarAssignTerm:
		return &ast.GrammarAssignTerm{Position: n.Position, Name: n.Name, Term: resolveGrammarTermTokenSets(n.Term, tokenSets)}
	case *ast.GrammarReturnTerm:
		return &ast.GrammarReturnTerm{Position: n.Position, Term: resolveGrammarTermTokenSets(n.Term, tokenSets)}
	case *ast.GrammarChoiceTerm:
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: resolveGrammarTermListTokenSets(n.Options, tokenSets)}
	case *ast.GrammarOptionalTerm:
		return &ast.GrammarOptionalTerm{Position: n.Position, Term: resolveGrammarTermTokenSets(n.Term, tokenSets)}
	case *ast.GrammarWhenTerm:
		return &ast.GrammarWhenTerm{Position: n.Position, Cond: n.Cond, Then: resolveGrammarTermTokenSets(n.Then, tokenSets), Else: resolveGrammarTermTokenSets(n.Else, tokenSets)}
	case *ast.GrammarRecoverTerm:
		return &ast.GrammarRecoverTerm{Position: n.Position, Term: resolveGrammarTermTokenSets(n.Term, tokenSets), RecoverPolicy: n.RecoverPolicy, RecoverMsg: n.RecoverMsg, RecoverUntil: resolveGrammarTokenSetRefsInStopList(n.RecoverUntil, tokenSets), RecoverValue: n.RecoverValue}
	case *ast.GrammarRequiredTerm:
		return &ast.GrammarRequiredTerm{Position: n.Position, Term: resolveGrammarTermTokenSets(n.Term, tokenSets), Message: n.Message}
	case *ast.GrammarDelimitedTerm:
		return &ast.GrammarDelimitedTerm{Position: n.Position, Open: resolveGrammarTermTokenSets(n.Open, tokenSets), Body: resolveGrammarTermTokenSets(n.Body, tokenSets), Close: resolveGrammarTermTokenSets(n.Close, tokenSets), Message: n.Message}
	case *ast.GrammarSeqTerm:
		return &ast.GrammarSeqTerm{Position: n.Position, Terms: resolveGrammarTermListTokenSets(n.Terms, tokenSets)}
	case *ast.GrammarLookaheadTerm:
		return &ast.GrammarLookaheadTerm{Position: n.Position, Term: resolveGrammarTermTokenSets(n.Term, tokenSets)}
	case *ast.GrammarSingletonTerm:
		return &ast.GrammarSingletonTerm{Position: n.Position, Type: n.Type, Value: n.Value}
	case *ast.GrammarEmptyTerm:
		return &ast.GrammarEmptyTerm{Position: n.Position, Type: n.Type}
	case *ast.GrammarConcatTerm:
		return &ast.GrammarConcatTerm{Position: n.Position, Terms: resolveGrammarTermListTokenSets(n.Terms, tokenSets)}
	case *ast.GrammarListTerm:
		return &ast.GrammarListTerm{Position: n.Position, Elem: resolveGrammarTermTokenSets(n.Elem, tokenSets), Separator: resolveGrammarTermTokenSets(n.Separator, tokenSets), Until: resolveGrammarTokenSetRefsInStopList(n.Until, tokenSets)}
	case *ast.GrammarRepeatTerm:
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: resolveGrammarTermTokenSets(n.Elem, tokenSets), Until: resolveGrammarTokenSetRefsInStopList(n.Until, tokenSets)}
	case *ast.GrammarFlatRepeatTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: resolveGrammarTermTokenSets(n.Elem, tokenSets), Until: resolveGrammarTokenSetRefsInStopList(n.Until, tokenSets)}
	case *ast.GrammarWhileTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: resolveGrammarTermTokenSets(n.Elem, tokenSets), Until: resolveGrammarTokenSetRefsInStopList(n.Until, tokenSets)}
	case *ast.GrammarSeparatedTerm:
		return &ast.GrammarSeparatedTerm{Position: n.Position, Elem: resolveGrammarTermTokenSets(n.Elem, tokenSets), Separator: resolveGrammarTermTokenSets(n.Separator, tokenSets), Until: resolveGrammarTokenSetRefsInStopList(n.Until, tokenSets)}
	case *ast.GrammarSuffixTerm:
		return &ast.GrammarSuffixTerm{Position: n.Position, LeftName: n.LeftName, Seed: resolveGrammarTermTokenSets(n.Seed, tokenSets), Arms: resolveGrammarPostfixArmsTokenSets(n.Arms, tokenSets)}
	case *ast.GrammarPostfixTerm:
		return &ast.GrammarPostfixTerm{Position: n.Position, LeftName: n.LeftName, Seed: resolveGrammarTermTokenSets(n.Seed, tokenSets), Arms: resolveGrammarPostfixArmsTokenSets(n.Arms, tokenSets)}
	case *ast.GrammarPrecedenceTerm:
		return &ast.GrammarPrecedenceTerm{Position: n.Position, Assoc: n.Assoc, Result: n.Result, Levels: resolveGrammarPrecedenceLevelsTokenSets(n.Levels, tokenSets), LeftName: n.LeftName, Seed: resolveGrammarTermTokenSets(n.Seed, tokenSets), Arms: resolveGrammarPrecedenceArmsTokenSets(n.Arms, tokenSets)}
	default:
		return term
	}
}

func resolveGrammarTermListTokenSets(terms []ast.GrammarTerm, tokenSets map[string]ast.GrammarTokenSetDecl) []ast.GrammarTerm {
	if len(terms) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarTerm, 0, len(terms))
	for _, term := range terms {
		resolved = append(resolved, resolveGrammarTermTokenSets(term, tokenSets))
	}
	return resolved
}

func resolveGrammarBindingsTokenSets(bindings []*ast.GrammarBindTerm, tokenSets map[string]ast.GrammarTokenSetDecl) []*ast.GrammarBindTerm {
	if len(bindings) == 0 {
		return nil
	}
	resolved := make([]*ast.GrammarBindTerm, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		resolved = append(resolved, &ast.GrammarBindTerm{Position: binding.Position, Name: binding.Name, Term: resolveGrammarTermTokenSets(binding.Term, tokenSets)})
	}
	return resolved
}

func resolveGrammarPostfixArmsTokenSets(arms []ast.GrammarPostfixArm, tokenSets map[string]ast.GrammarTokenSetDecl) []ast.GrammarPostfixArm {
	if len(arms) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarPostfixArm, 0, len(arms))
	for _, arm := range arms {
		resolved = append(resolved, ast.GrammarPostfixArm{Position: arm.Position, OpName: arm.OpName, Op: resolveGrammarTermTokenSets(arm.Op, tokenSets), Block: arm.Block, Bindings: resolveGrammarBindingsTokenSets(arm.Bindings, tokenSets), Value: arm.Value})
	}
	return resolved
}

func resolveGrammarPrecedenceArmsTokenSets(arms []ast.GrammarPrecedenceArm, tokenSets map[string]ast.GrammarTokenSetDecl) []ast.GrammarPrecedenceArm {
	if len(arms) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarPrecedenceArm, 0, len(arms))
	for _, arm := range arms {
		resolved = append(resolved, ast.GrammarPrecedenceArm{Position: arm.Position, OpName: arm.OpName, Op: resolveGrammarTermTokenSets(arm.Op, tokenSets), Block: arm.Block, Bindings: resolveGrammarBindingsTokenSets(arm.Bindings, tokenSets), Value: arm.Value})
	}
	return resolved
}

func resolveGrammarPrecedenceLevelsTokenSets(levels []ast.GrammarPrecedenceLevel, tokenSets map[string]ast.GrammarTokenSetDecl) []ast.GrammarPrecedenceLevel {
	if len(levels) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarPrecedenceLevel, 0, len(levels))
	for _, level := range levels {
		resolved = append(resolved, ast.GrammarPrecedenceLevel{Position: level.Position, Assoc: level.Assoc, Name: level.Name, LeftName: level.LeftName, Seed: resolveGrammarTermTokenSets(level.Seed, tokenSets), Arms: resolveGrammarPrecedenceArmsTokenSets(level.Arms, tokenSets)})
	}
	return resolved
}
