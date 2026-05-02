package grammar

import "llcontext/src/ast"

func grammarRecoveryPolicyMap(policies []ast.GrammarRecoveryDecl) map[string]ast.GrammarRecoveryDecl {
	if len(policies) == 0 {
		return nil
	}
	resolved := make(map[string]ast.GrammarRecoveryDecl, len(policies))
	for _, policy := range policies {
		if policy.Name == "" {
			continue
		}
		resolved[policy.Name] = policy
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

func resolveGrammarProductionRecoveryPolicies(production ast.GrammarProductionDecl, policies map[string]ast.GrammarRecoveryDecl) ast.GrammarProductionDecl {
	production.RecoverMsg, production.RecoverUntil, production.RecoverValue = resolveGrammarRecoveryPolicy(production.RecoverPolicy, production.RecoverMsg, production.RecoverUntil, production.RecoverValue, policies)
	production.RecoverPolicy = ""
	if len(production.Terms) == 0 {
		return production
	}
	terms := make([]ast.GrammarTerm, 0, len(production.Terms))
	for _, term := range production.Terms {
		terms = append(terms, resolveGrammarTermRecoveryPolicies(term, policies))
	}
	production.Terms = terms
	return production
}

func resolveGrammarRecoveryPolicy(name string, message ast.Expr, until []ast.GrammarTerm, fallback ast.Expr, policies map[string]ast.GrammarRecoveryDecl) (ast.Expr, []ast.GrammarTerm, ast.Expr) {
	if name == "" || len(policies) == 0 {
		return message, until, fallback
	}
	policy, ok := policies[name]
	if !ok {
		return message, until, fallback
	}
	return policy.Message, append([]ast.GrammarTerm(nil), policy.Until...), policy.Fallback
}

func resolveGrammarTermRecoveryPolicies(term ast.GrammarTerm, policies map[string]ast.GrammarRecoveryDecl) ast.GrammarTerm {
	if term == nil {
		return nil
	}
	switch n := term.(type) {
	case *ast.GrammarBindTerm:
		return &ast.GrammarBindTerm{Position: n.Position, Name: n.Name, Term: resolveGrammarTermRecoveryPolicies(n.Term, policies)}
	case *ast.GrammarAssignTerm:
		return &ast.GrammarAssignTerm{Position: n.Position, Name: n.Name, Term: resolveGrammarTermRecoveryPolicies(n.Term, policies)}
	case *ast.GrammarReturnTerm:
		return &ast.GrammarReturnTerm{Position: n.Position, Term: resolveGrammarTermRecoveryPolicies(n.Term, policies)}
	case *ast.GrammarChoiceTerm:
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: resolveGrammarTermListRecoveryPolicies(n.Options, policies)}
	case *ast.GrammarOptionalTerm:
		return &ast.GrammarOptionalTerm{Position: n.Position, Term: resolveGrammarTermRecoveryPolicies(n.Term, policies)}
	case *ast.GrammarWhenTerm:
		return &ast.GrammarWhenTerm{Position: n.Position, Cond: n.Cond, Then: resolveGrammarTermRecoveryPolicies(n.Then, policies), Else: resolveGrammarTermRecoveryPolicies(n.Else, policies)}
	case *ast.GrammarRecoverTerm:
		message, until, fallback := resolveGrammarRecoveryPolicy(n.RecoverPolicy, n.RecoverMsg, n.RecoverUntil, n.RecoverValue, policies)
		return &ast.GrammarRecoverTerm{Position: n.Position, Term: resolveGrammarTermRecoveryPolicies(n.Term, policies), RecoverMsg: message, RecoverUntil: until, RecoverValue: fallback}
	case *ast.GrammarRequiredTerm:
		return &ast.GrammarRequiredTerm{Position: n.Position, Term: resolveGrammarTermRecoveryPolicies(n.Term, policies), Message: n.Message}
	case *ast.GrammarDelimitedTerm:
		return &ast.GrammarDelimitedTerm{Position: n.Position, Open: resolveGrammarTermRecoveryPolicies(n.Open, policies), Body: resolveGrammarTermRecoveryPolicies(n.Body, policies), Close: resolveGrammarTermRecoveryPolicies(n.Close, policies), Message: n.Message}
	case *ast.GrammarSeqTerm:
		return &ast.GrammarSeqTerm{Position: n.Position, Terms: resolveGrammarTermListRecoveryPolicies(n.Terms, policies)}
	case *ast.GrammarLookaheadTerm:
		return &ast.GrammarLookaheadTerm{Position: n.Position, Term: resolveGrammarTermRecoveryPolicies(n.Term, policies)}
	case *ast.GrammarSingletonTerm:
		return &ast.GrammarSingletonTerm{Position: n.Position, Type: n.Type, Value: n.Value}
	case *ast.GrammarEmptyTerm:
		return &ast.GrammarEmptyTerm{Position: n.Position, Type: n.Type}
	case *ast.GrammarConcatTerm:
		return &ast.GrammarConcatTerm{Position: n.Position, Terms: resolveGrammarTermListRecoveryPolicies(n.Terms, policies)}
	case *ast.GrammarListTerm:
		return &ast.GrammarListTerm{Position: n.Position, Elem: resolveGrammarTermRecoveryPolicies(n.Elem, policies), Separator: resolveGrammarTermRecoveryPolicies(n.Separator, policies), Until: resolveGrammarTermListRecoveryPolicies(n.Until, policies)}
	case *ast.GrammarRepeatTerm:
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: resolveGrammarTermRecoveryPolicies(n.Elem, policies), Until: resolveGrammarTermListRecoveryPolicies(n.Until, policies)}
	case *ast.GrammarFlatRepeatTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: resolveGrammarTermRecoveryPolicies(n.Elem, policies), Until: resolveGrammarTermListRecoveryPolicies(n.Until, policies)}
	case *ast.GrammarWhileTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: resolveGrammarTermRecoveryPolicies(n.Elem, policies), Until: resolveGrammarTermListRecoveryPolicies(n.Until, policies)}
	case *ast.GrammarSeparatedTerm:
		return &ast.GrammarSeparatedTerm{Position: n.Position, Elem: resolveGrammarTermRecoveryPolicies(n.Elem, policies), Separator: resolveGrammarTermRecoveryPolicies(n.Separator, policies), Until: resolveGrammarTermListRecoveryPolicies(n.Until, policies)}
	case *ast.GrammarSuffixTerm:
		return &ast.GrammarSuffixTerm{Position: n.Position, LeftName: n.LeftName, Seed: resolveGrammarTermRecoveryPolicies(n.Seed, policies), Arms: resolveGrammarPostfixArmsRecoveryPolicies(n.Arms, policies)}
	case *ast.GrammarPostfixTerm:
		return &ast.GrammarPostfixTerm{Position: n.Position, LeftName: n.LeftName, Seed: resolveGrammarTermRecoveryPolicies(n.Seed, policies), Arms: resolveGrammarPostfixArmsRecoveryPolicies(n.Arms, policies)}
	case *ast.GrammarPrecedenceTerm:
		return &ast.GrammarPrecedenceTerm{Position: n.Position, Assoc: n.Assoc, Result: n.Result, Levels: resolveGrammarPrecedenceLevelsRecoveryPolicies(n.Levels, policies), LeftName: n.LeftName, Seed: resolveGrammarTermRecoveryPolicies(n.Seed, policies), Arms: resolveGrammarPrecedenceArmsRecoveryPolicies(n.Arms, policies)}
	default:
		return term
	}
}

func resolveGrammarTermListRecoveryPolicies(terms []ast.GrammarTerm, policies map[string]ast.GrammarRecoveryDecl) []ast.GrammarTerm {
	if len(terms) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarTerm, 0, len(terms))
	for _, term := range terms {
		resolved = append(resolved, resolveGrammarTermRecoveryPolicies(term, policies))
	}
	return resolved
}

func resolveGrammarBindingsRecoveryPolicies(bindings []*ast.GrammarBindTerm, policies map[string]ast.GrammarRecoveryDecl) []*ast.GrammarBindTerm {
	if len(bindings) == 0 {
		return nil
	}
	resolved := make([]*ast.GrammarBindTerm, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		resolved = append(resolved, &ast.GrammarBindTerm{Position: binding.Position, Name: binding.Name, Term: resolveGrammarTermRecoveryPolicies(binding.Term, policies)})
	}
	return resolved
}

func resolveGrammarPostfixArmsRecoveryPolicies(arms []ast.GrammarPostfixArm, policies map[string]ast.GrammarRecoveryDecl) []ast.GrammarPostfixArm {
	if len(arms) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarPostfixArm, 0, len(arms))
	for _, arm := range arms {
		resolved = append(resolved, ast.GrammarPostfixArm{Position: arm.Position, OpName: arm.OpName, Op: resolveGrammarTermRecoveryPolicies(arm.Op, policies), Block: arm.Block, Bindings: resolveGrammarBindingsRecoveryPolicies(arm.Bindings, policies), Value: arm.Value})
	}
	return resolved
}

func resolveGrammarPrecedenceArmsRecoveryPolicies(arms []ast.GrammarPrecedenceArm, policies map[string]ast.GrammarRecoveryDecl) []ast.GrammarPrecedenceArm {
	if len(arms) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarPrecedenceArm, 0, len(arms))
	for _, arm := range arms {
		resolved = append(resolved, ast.GrammarPrecedenceArm{Position: arm.Position, OpName: arm.OpName, Op: resolveGrammarTermRecoveryPolicies(arm.Op, policies), Block: arm.Block, Bindings: resolveGrammarBindingsRecoveryPolicies(arm.Bindings, policies), Value: arm.Value})
	}
	return resolved
}

func resolveGrammarPrecedenceLevelsRecoveryPolicies(levels []ast.GrammarPrecedenceLevel, policies map[string]ast.GrammarRecoveryDecl) []ast.GrammarPrecedenceLevel {
	if len(levels) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarPrecedenceLevel, 0, len(levels))
	for _, level := range levels {
		resolved = append(resolved, ast.GrammarPrecedenceLevel{Position: level.Position, Assoc: level.Assoc, Name: level.Name, LeftName: level.LeftName, Seed: resolveGrammarTermRecoveryPolicies(level.Seed, policies), Arms: resolveGrammarPrecedenceArmsRecoveryPolicies(level.Arms, policies)})
	}
	return resolved
}
