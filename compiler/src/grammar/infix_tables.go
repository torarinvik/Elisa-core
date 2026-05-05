package grammar

import "elisacore/src/ast"

func grammarInfixTableMap(tables []ast.GrammarInfixTableDecl) map[string]ast.GrammarInfixTableDecl {
	if len(tables) == 0 {
		return nil
	}
	resolved := make(map[string]ast.GrammarInfixTableDecl, len(tables))
	for _, table := range tables {
		if table.Name == "" {
			continue
		}
		resolved[table.Name] = table
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

func resolveGrammarProductionInfixTables(production ast.GrammarProductionDecl, tables map[string]ast.GrammarInfixTableDecl) ast.GrammarProductionDecl {
	if len(production.Terms) == 0 {
		return production
	}
	terms := make([]ast.GrammarTerm, 0, len(production.Terms))
	for _, term := range production.Terms {
		terms = append(terms, resolveGrammarTermInfixTables(term, tables))
	}
	production.Terms = terms
	return production
}

func resolveGrammarTermInfixTables(term ast.GrammarTerm, tables map[string]ast.GrammarInfixTableDecl) ast.GrammarTerm {
	if term == nil {
		return nil
	}
	switch n := term.(type) {
	case *ast.GrammarBindTerm:
		return &ast.GrammarBindTerm{Position: n.Position, Name: n.Name, Term: resolveGrammarTermInfixTables(n.Term, tables)}
	case *ast.GrammarAssignTerm:
		return &ast.GrammarAssignTerm{Position: n.Position, Name: n.Name, Term: resolveGrammarTermInfixTables(n.Term, tables)}
	case *ast.GrammarReturnTerm:
		return &ast.GrammarReturnTerm{Position: n.Position, Term: resolveGrammarTermInfixTables(n.Term, tables)}
	case *ast.GrammarChoiceTerm:
		return &ast.GrammarChoiceTerm{Position: n.Position, Options: resolveGrammarTermListInfixTables(n.Options, tables)}
	case *ast.GrammarOptionalTerm:
		return &ast.GrammarOptionalTerm{Position: n.Position, Term: resolveGrammarTermInfixTables(n.Term, tables)}
	case *ast.GrammarWhenTerm:
		return &ast.GrammarWhenTerm{Position: n.Position, Cond: n.Cond, TokenKindGate: n.TokenKindGate, Then: resolveGrammarTermInfixTables(n.Then, tables), Else: resolveGrammarTermInfixTables(n.Else, tables)}
	case *ast.GrammarRecoverTerm:
		return &ast.GrammarRecoverTerm{Position: n.Position, Term: resolveGrammarTermInfixTables(n.Term, tables), RecoverPolicy: n.RecoverPolicy, RecoverMsg: n.RecoverMsg, RecoverUntil: resolveGrammarTermListInfixTables(n.RecoverUntil, tables), RecoverValue: n.RecoverValue}
	case *ast.GrammarRequiredTerm:
		return &ast.GrammarRequiredTerm{Position: n.Position, Term: resolveGrammarTermInfixTables(n.Term, tables), Message: n.Message}
	case *ast.GrammarDelimitedTerm:
		return &ast.GrammarDelimitedTerm{Position: n.Position, Open: resolveGrammarTermInfixTables(n.Open, tables), Body: resolveGrammarTermInfixTables(n.Body, tables), Close: resolveGrammarTermInfixTables(n.Close, tables), Message: n.Message}
	case *ast.GrammarSeqTerm:
		return &ast.GrammarSeqTerm{Position: n.Position, Terms: resolveGrammarTermListInfixTables(n.Terms, tables)}
	case *ast.GrammarLookaheadTerm:
		return &ast.GrammarLookaheadTerm{Position: n.Position, Term: resolveGrammarTermInfixTables(n.Term, tables)}
	case *ast.GrammarSingletonTerm:
		return &ast.GrammarSingletonTerm{Position: n.Position, Type: n.Type, Value: n.Value}
	case *ast.GrammarEmptyTerm:
		return &ast.GrammarEmptyTerm{Position: n.Position, Type: n.Type}
	case *ast.GrammarConcatTerm:
		return &ast.GrammarConcatTerm{Position: n.Position, Terms: resolveGrammarTermListInfixTables(n.Terms, tables)}
	case *ast.GrammarListTerm:
		return &ast.GrammarListTerm{Position: n.Position, Elem: resolveGrammarTermInfixTables(n.Elem, tables), Separator: resolveGrammarTermInfixTables(n.Separator, tables), Until: resolveGrammarTermListInfixTables(n.Until, tables)}
	case *ast.GrammarRepeatTerm:
		return &ast.GrammarRepeatTerm{Position: n.Position, Elem: resolveGrammarTermInfixTables(n.Elem, tables), Until: resolveGrammarTermListInfixTables(n.Until, tables)}
	case *ast.GrammarFlatRepeatTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: resolveGrammarTermInfixTables(n.Elem, tables), Until: resolveGrammarTermListInfixTables(n.Until, tables)}
	case *ast.GrammarWhileTerm:
		return &ast.GrammarFlatRepeatTerm{Position: n.Position, Elem: resolveGrammarTermInfixTables(n.Elem, tables), Until: resolveGrammarTermListInfixTables(n.Until, tables)}
	case *ast.GrammarSeparatedTerm:
		return &ast.GrammarSeparatedTerm{Position: n.Position, Elem: resolveGrammarTermInfixTables(n.Elem, tables), Separator: resolveGrammarTermInfixTables(n.Separator, tables), Until: resolveGrammarTermListInfixTables(n.Until, tables)}
	case *ast.GrammarSuffixTerm:
		return &ast.GrammarSuffixTerm{Position: n.Position, LeftName: n.LeftName, Seed: resolveGrammarTermInfixTables(n.Seed, tables), Arms: resolveGrammarPostfixArmsInfixTables(n.Arms, tables)}
	case *ast.GrammarPostfixTerm:
		return &ast.GrammarPostfixTerm{Position: n.Position, LeftName: n.LeftName, Seed: resolveGrammarTermInfixTables(n.Seed, tables), Arms: resolveGrammarPostfixArmsInfixTables(n.Arms, tables)}
	case *ast.GrammarPrecedenceTerm:
		return &ast.GrammarPrecedenceTerm{Position: n.Position, Assoc: n.Assoc, Result: n.Result, Levels: resolveGrammarPrecedenceLevelsInfixTables(n.Levels, tables), LeftName: n.LeftName, Seed: resolveGrammarTermInfixTables(n.Seed, tables), Arms: resolveGrammarPrecedenceArmsInfixTables(n.Arms, tables)}
	case *ast.GrammarInfixTableTerm:
		table, ok := tables[n.TableName]
		if !ok {
			return n
		}
		return &ast.GrammarPrecedenceTerm{Position: n.Position, Result: table.Result, Levels: resolveGrammarPrecedenceLevelsInfixTables(table.Levels, tables)}
	default:
		return term
	}
}

func resolveGrammarTermListInfixTables(terms []ast.GrammarTerm, tables map[string]ast.GrammarInfixTableDecl) []ast.GrammarTerm {
	if len(terms) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarTerm, 0, len(terms))
	for _, term := range terms {
		resolved = append(resolved, resolveGrammarTermInfixTables(term, tables))
	}
	return resolved
}

func resolveGrammarBindingsInfixTables(bindings []*ast.GrammarBindTerm, tables map[string]ast.GrammarInfixTableDecl) []*ast.GrammarBindTerm {
	if len(bindings) == 0 {
		return nil
	}
	resolved := make([]*ast.GrammarBindTerm, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		resolved = append(resolved, &ast.GrammarBindTerm{Position: binding.Position, Name: binding.Name, Term: resolveGrammarTermInfixTables(binding.Term, tables)})
	}
	return resolved
}

func resolveGrammarPostfixArmsInfixTables(arms []ast.GrammarPostfixArm, tables map[string]ast.GrammarInfixTableDecl) []ast.GrammarPostfixArm {
	if len(arms) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarPostfixArm, 0, len(arms))
	for _, arm := range arms {
		resolved = append(resolved, ast.GrammarPostfixArm{Position: arm.Position, OpName: arm.OpName, Op: resolveGrammarTermInfixTables(arm.Op, tables), Block: arm.Block, Bindings: resolveGrammarBindingsInfixTables(arm.Bindings, tables), Value: arm.Value})
	}
	return resolved
}

func resolveGrammarPrecedenceArmsInfixTables(arms []ast.GrammarPrecedenceArm, tables map[string]ast.GrammarInfixTableDecl) []ast.GrammarPrecedenceArm {
	if len(arms) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarPrecedenceArm, 0, len(arms))
	for _, arm := range arms {
		resolved = append(resolved, ast.GrammarPrecedenceArm{Position: arm.Position, OpName: arm.OpName, Op: resolveGrammarTermInfixTables(arm.Op, tables), Block: arm.Block, Bindings: resolveGrammarBindingsInfixTables(arm.Bindings, tables), Value: arm.Value})
	}
	return resolved
}

func resolveGrammarPrecedenceLevelsInfixTables(levels []ast.GrammarPrecedenceLevel, tables map[string]ast.GrammarInfixTableDecl) []ast.GrammarPrecedenceLevel {
	if len(levels) == 0 {
		return nil
	}
	resolved := make([]ast.GrammarPrecedenceLevel, 0, len(levels))
	for _, level := range levels {
		resolved = append(resolved, ast.GrammarPrecedenceLevel{Position: level.Position, Assoc: level.Assoc, Name: level.Name, LeftName: level.LeftName, Seed: resolveGrammarTermInfixTables(level.Seed, tables), Arms: resolveGrammarPrecedenceArmsInfixTables(level.Arms, tables)})
	}
	return resolved
}
