package grammar

import "elisacore/src/ast"

func inferLowerContextTermType(term ast.GrammarTerm, fallback ast.TypeExpr) ast.TypeExpr {
	switch n := term.(type) {
	case *ast.GrammarTokenTerm:
		return &ast.NamedType{Position: n.Position, Name: "Token"}
	case *ast.GrammarTokenKindTerm:
		return &ast.NamedType{Position: n.Position, Name: "Token"}
	case *ast.GrammarCallTerm:
		if _, ok := grammarTokenKindMatcher(n); ok {
			return &ast.NamedType{Position: n.Position, Name: "Token"}
		}
		return fallback
	case *ast.GrammarChoiceTerm:
		for _, option := range n.Options {
			if typ := inferLowerContextTermType(option, fallback); typ != nil {
				return typ
			}
		}
		return fallback
	case *ast.GrammarWhenTerm:
		if typ := inferLowerContextTermType(n.Then, fallback); typ != nil {
			return typ
		}
		return inferLowerContextTermType(n.Else, fallback)
	case *ast.GrammarRequiredTerm:
		return inferLowerContextTermType(n.Term, fallback)
	case *ast.GrammarDelimitedTerm:
		return inferLowerContextTermType(n.Body, fallback)
	case *ast.GrammarSeqTerm:
		if len(n.Terms) == 0 {
			return builtinTypeExpr(n.Position, "bool")
		}
		return inferLowerContextTermType(n.Terms[len(n.Terms)-1], fallback)
	case *ast.GrammarLookaheadTerm:
		return inferLowerContextTermType(n.Term, fallback)
	case *ast.GrammarExprTerm:
		if n.Type != nil {
			return n.Type
		}
		return fallback
	case *ast.GrammarSingletonTerm:
		return listTypeExpr(n.Position, grammarListElementType(n.Position, n.Type, fallback))
	case *ast.GrammarEmptyTerm:
		return listTypeExpr(n.Position, grammarListElementType(n.Position, n.Type, fallback))
	case *ast.GrammarConcatTerm:
		for _, child := range n.Terms {
			if typ := inferLowerContextTermType(child, fallback); typ != nil {
				return typ
			}
		}
		return fallback
	case *ast.GrammarRecoverTerm:
		return inferLowerContextTermType(n.Term, fallback)
	case *ast.GrammarGuardTerm:
		return builtinTypeExpr(n.Position, "bool")
	case *ast.GrammarAttemptTerm:
		return nil
	case *ast.GrammarCutTerm:
		return builtinTypeExpr(n.Position, "bool")
	case *ast.GrammarOptionalTerm:
		return inferLowerContextTermType(n.Term, fallback)
	case *ast.GrammarListTerm:
		return listTypeExpr(n.Position, inferLowerContextTermType(n.Elem, fallback))
	case *ast.GrammarDynamicClimbTerm:
		return n.Spec.ReturnType
	case *ast.GrammarRepeatTerm:
		return listTypeExpr(n.Position, inferLowerContextTermType(n.Elem, fallback))
	case *ast.GrammarFlatRepeatTerm:
		return inferLowerContextTermType(n.Elem, fallback)
	case *ast.GrammarSeparatedTerm:
		return listTypeExpr(n.Position, inferLowerContextTermType(n.Elem, fallback))
	case *ast.GrammarSuffixTerm:
		return inferLowerContextTermType(n.Seed, fallback)
	case *ast.GrammarPostfixTerm:
		return inferLowerContextTermType(n.Seed, fallback)
	case *ast.GrammarPrecedenceTerm:
		if len(n.Levels) != 0 {
			return fallback
		}
		return inferLowerContextTermType(n.Seed, fallback)
	case *ast.GrammarBindTerm:
		return inferLowerContextTermType(n.Term, fallback)
	case *ast.GrammarReturnTerm:
		return inferLowerContextTermType(n.Term, fallback)
	case *ast.GrammarPassTerm:
		return fallback
	default:
		return fallback
	}
}
