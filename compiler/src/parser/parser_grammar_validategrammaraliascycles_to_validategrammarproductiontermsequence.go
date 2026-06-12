package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) validateGrammarAliasCycles(aliases []ast.GrammarAliasDecl) {
	if len(aliases) == 0 {
		return
	}
	aliasMap := make(map[string]ast.GrammarAliasDecl, len(aliases))
	for _, alias := range aliases {
		if alias.Name != "" {
			if existing, exists := aliasMap[alias.Name]; exists {
				p.errorAt(alias.Position, "duplicate grammar alias %q; first declared at %s", alias.Name, existing.Position)
			}
			aliasMap[alias.Name] = alias
		}
	}
	for _, alias := range aliases {
		p.validateGrammarAliasCycle(alias, aliasMap, nil)
	}
}
func (p *Parser) validateGrammarAliasCycle(alias ast.GrammarAliasDecl, aliases map[string]ast.GrammarAliasDecl, stack []string) {
	for _, name := range stack {
		if name == alias.Name {
			p.errorAt(alias.Position, "recursive grammar alias %q", alias.Name)
			return
		}
	}
	stack = append(stack, alias.Name)
	for _, ref := range grammarAliasRefs(alias.Term, aliases) {
		next, ok := aliases[ref]
		if !ok {
			continue
		}
		p.validateGrammarAliasCycle(next, aliases, stack)
	}
}
func grammarAliasRefs(term ast.GrammarTerm, aliases map[string]ast.GrammarAliasDecl) []string {
	refs := make([]string, 0, 2)
	var walk func(ast.GrammarTerm)
	walk = func(term ast.GrammarTerm) {
		switch n := term.(type) {
		case *ast.GrammarCallTerm:
			if !n.Explicit && len(n.Args) == 0 {
				if _, ok := aliases[n.Name]; ok {
					refs = append(refs, n.Name)
				}
			}
		case *ast.GrammarApplyTerm:
			for _, arg := range n.Args {
				walk(arg.Term)
			}
		case *ast.GrammarBindTerm:
			walk(n.Term)
		case *ast.GrammarAssignTerm:
			walk(n.Term)
		case *ast.GrammarReturnTerm:
			walk(n.Term)
		case *ast.GrammarChoiceTerm:
			for _, option := range n.Options {
				walk(option)
			}
		case *ast.GrammarOptionalTerm:
			walk(n.Term)
		case *ast.GrammarWhenTerm:
			walk(n.Then)
			walk(n.Else)
		case *ast.GrammarMatchTerm:
			for _, arm := range n.Arms {
				walk(arm.Term)
			}
		case *ast.GrammarRecoverTerm:
			walk(n.Term)
			for _, until := range n.RecoverUntil {
				walk(until)
			}
		case *ast.GrammarRequiredTerm:
			walk(n.Term)
		case *ast.GrammarDelimitedTerm:
			walk(n.Open)
			walk(n.Body)
			walk(n.Close)
		case *ast.GrammarSeqTerm:
			for _, item := range n.Terms {
				walk(item)
			}
		case *ast.GrammarLookaheadTerm:
			walk(n.Term)
		case *ast.GrammarConcatTerm:
			for _, item := range n.Terms {
				walk(item)
			}
		case *ast.GrammarListTerm:
			walk(n.Elem)
			walk(n.Separator)
			for _, until := range n.Until {
				walk(until)
			}
		case *ast.GrammarRepeatTerm:
			walk(n.Elem)
			for _, until := range n.Until {
				walk(until)
			}
		case *ast.GrammarFlatRepeatTerm:
			walk(n.Elem)
			for _, until := range n.Until {
				walk(until)
			}
		case *ast.GrammarSeparatedTerm:
			walk(n.Elem)
			walk(n.Separator)
			for _, until := range n.Until {
				walk(until)
			}
		case *ast.GrammarSuffixTerm:
			walk(n.Seed)
			for _, arm := range n.Arms {
				walk(arm.Op)
				for _, binding := range arm.Bindings {
					if binding != nil {
						walk(binding.Term)
					}
				}
			}
		case *ast.GrammarPostfixTerm:
			walk(n.Seed)
			for _, arm := range n.Arms {
				walk(arm.Op)
				for _, binding := range arm.Bindings {
					if binding != nil {
						walk(binding.Term)
					}
				}
			}
		case *ast.GrammarPrecedenceTerm:
			walk(n.Seed)
			for _, level := range n.Levels {
				walk(level.Seed)
				for _, arm := range level.Arms {
					walk(arm.Op)
					for _, binding := range arm.Bindings {
						if binding != nil {
							walk(binding.Term)
						}
					}
				}
			}
			for _, arm := range n.Arms {
				walk(arm.Op)
				for _, binding := range arm.Bindings {
					if binding != nil {
						walk(binding.Term)
					}
				}
			}
		}
	}
	walk(term)
	return refs
}
func (p *Parser) parseGrammarFnDecl(typeCtor bool) ast.GrammarFnDecl {
	pos := p.cur().Pos
	if typeCtor {
		p.expectIdentText("grammar")
		p.expectIdentText("type")
	} else {
		p.expectIdentText("grammarfn")
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeParams, _, _, genericParams := p.parseFuncGenericParams()
	p.expect(lexer.TOKEN_LPAREN)
	params := p.parseGrammarFnParamsUntilRParen()
	var ret ast.GrammarFnType
	if p.match(lexer.TOKEN_ARROW) {
		ret = p.parseGrammarFnType()
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	terms := p.parseGrammarTermBlock()
	return ast.GrammarFnDecl{Position: pos, Name: name, TypeCtor: typeCtor, TypeParams: typeParams, GenericParams: genericParams, Params: params, Return: ret, Terms: terms}
}
func (p *Parser) parseGrammarHelperDecl() ast.GrammarFnDecl {
	pos := p.cur().Pos
	p.expectIdentText("grammar")
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeParams, _, _, genericParams := p.parseFuncGenericParams()
	p.expect(lexer.TOKEN_LPAREN)
	params := p.parseGrammarFnParamsUntilRParen()
	if !p.match(lexer.TOKEN_ARROW) {
		p.errorAt(pos, "expected return type for grammar helper %q; use `grammar %s(...) -> ResultType:`", name, name)
	}
	retType := p.parseTypeExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	terms := p.parseGrammarTermBlock()
	return ast.GrammarFnDecl{Position: pos, Name: name, TypeCtor: true, Shorthand: true, TypeParams: typeParams, GenericParams: genericParams, Params: params, Return: ast.GrammarFnType{Position: pos, Kind: "grammar", Result: retType}, Terms: terms}
}
func (p *Parser) parseGrammarFnParamsUntilRParen() []ast.GrammarFnParam {
	params := make([]ast.GrammarFnParam, 0, 4)
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			paramPos := p.cur().Pos
			paramName := p.expect(lexer.TOKEN_IDENT).Text
			var paramType ast.GrammarFnType
			if p.match(lexer.TOKEN_COLON) {
				paramType = p.parseGrammarFnType()
			}
			var defaultTerm ast.GrammarTerm
			var defaultExpr ast.Expr
			if p.match(lexer.TOKEN_ASSIGN) {
				if paramType.Kind == "expr" {
					defaultExpr = p.parseExpr()
				} else {
					defaultTerm = p.parseGrammarUntilStopTerm()
				}
			}
			params = append(params, ast.GrammarFnParam{Position: paramPos, Name: paramName, Type: paramType, Default: defaultTerm, DefaultExpr: defaultExpr})
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return params
}
func (p *Parser) parseGrammarFnType() ast.GrammarFnType {
	pos := p.cur().Pos
	kind := p.expect(lexer.TOKEN_IDENT).Text
	typ := ast.GrammarFnType{Position: pos, Kind: kind}
	switch kind {
	case "grammar":
		if p.match(lexer.TOKEN_ARROW) {
			typ.Result = p.parseTypeExpr()
		}
	case "tokenset", "expr":
	default:
		p.errorAt(pos, "expected grammar function type `grammar`, `tokenset`, or `expr`, got %q", kind)
	}
	return typ
}
func (p *Parser) validateGrammarFnApplications(grammarFns []ast.GrammarFnDecl, aliases []ast.GrammarAliasDecl, tokenSets []ast.GrammarTokenSetDecl, productions []ast.GrammarProductionDecl) {
	if len(grammarFns) == 0 && len(aliases) == 0 {
		return
	}
	fnMap := make(map[string]ast.GrammarFnDecl, len(grammarFns))
	for _, grammarFn := range grammarFns {
		if grammarFn.Name != "" {
			fnMap[grammarFn.Name] = grammarFn
		}
	}
	aliasMap := make(map[string]ast.GrammarAliasDecl, len(aliases))
	for _, alias := range aliases {
		if alias.Name != "" && len(alias.Params) != 0 {
			aliasMap[alias.Name] = alias
		}
	}
	tokenSetNames := make(map[string]bool, len(tokenSets))
	for _, tokenSet := range tokenSets {
		if tokenSet.Name != "" {
			tokenSetNames[tokenSet.Name] = true
		}
	}
	for _, grammarFn := range grammarFns {
		localTokenSetNames := grammarTokenSetNamesWithParams(tokenSetNames, grammarFn.Params)
		for _, param := range grammarFn.Params {
			if param.Default != nil {
				p.validateGrammarFnApplicationInTerm(param.Default, fnMap, aliasMap, localTokenSetNames)
			}
		}
		p.validateGrammarFnApplicationsInTerms(grammarFn.Terms, fnMap, aliasMap, localTokenSetNames)
	}
	for _, alias := range aliases {
		localTokenSetNames := grammarTokenSetNamesWithParams(tokenSetNames, alias.Params)
		for _, param := range alias.Params {
			if param.Default != nil {
				p.validateGrammarFnApplicationInTerm(param.Default, nil, aliasMap, localTokenSetNames)
			}
		}
		p.validateGrammarFnApplicationInTerm(alias.Term, nil, aliasMap, localTokenSetNames)
	}
	for _, production := range productions {
		p.validateGrammarFnApplicationsInTerms(production.Terms, fnMap, aliasMap, tokenSetNames)
		p.validateGrammarFnApplicationsInTerms(production.RecoverUntil, fnMap, aliasMap, tokenSetNames)
	}
}
func grammarTokenSetNamesWithParams(tokenSetNames map[string]bool, params []ast.GrammarFnParam) map[string]bool {
	localTokenSetNames := tokenSetNames
	copiedTokenSetNames := false
	for _, param := range params {
		if param.Type.Kind != "tokenset" {
			continue
		}
		if !copiedTokenSetNames {
			localTokenSetNames = make(map[string]bool, len(tokenSetNames)+1)
			for name, isTokenSet := range tokenSetNames {
				localTokenSetNames[name] = isTokenSet
			}
			copiedTokenSetNames = true
		}
		localTokenSetNames[param.Name] = true
	}
	return localTokenSetNames
}
func (p *Parser) validateGrammarFnApplicationsInTerms(terms []ast.GrammarTerm, grammarFns map[string]ast.GrammarFnDecl, aliases map[string]ast.GrammarAliasDecl, tokenSetNames map[string]bool) {
	for _, term := range terms {
		p.validateGrammarFnApplicationInTerm(term, grammarFns, aliases, tokenSetNames)
	}
}
func (p *Parser) validateGrammarFnApplicationInTerm(term ast.GrammarTerm, grammarFns map[string]ast.GrammarFnDecl, aliases map[string]ast.GrammarAliasDecl, tokenSetNames map[string]bool) {
	switch n := term.(type) {
	case *ast.GrammarApplyTerm:
		grammarFn, ok := grammarFns[n.Name]
		if ok {
			resolved, ok := p.resolveGrammarFnApplyArgs(n, grammarFn)
			if ok {
				p.validateGrammarApplyArgTypes("grammarfn", n.Name, grammarFn.Params, resolved, tokenSetNames)
			}
		} else if alias, ok := aliases[n.Name]; ok {
			resolved, ok := p.resolveGrammarAliasApplyArgs(n, alias)
			if ok {
				p.validateGrammarApplyArgTypes("grammar alias", n.Name, alias.Params, resolved, tokenSetNames)
			}
		} else if len(grammarFns) != 0 {
			p.errorAt(n.Position, "unknown grammar function %q", n.Name)
		}
		for _, arg := range n.Args {
			p.validateGrammarFnApplicationInTerm(arg.Term, grammarFns, aliases, tokenSetNames)
		}
	case *ast.GrammarBindTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarAssignTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarReturnTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarChoiceTerm:
		p.validateGrammarFnApplicationsInTerms(n.Options, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarOptionalTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarWhenTerm:
		p.validateGrammarFnApplicationInTerm(n.Then, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationInTerm(n.Else, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarMatchTerm:
		for _, arm := range n.Arms {
			p.validateGrammarFnApplicationInTerm(arm.Term, grammarFns, aliases, tokenSetNames)
		}
	case *ast.GrammarRecoverTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationsInTerms(n.RecoverUntil, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarRequiredTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarDelimitedTerm:
		p.validateGrammarFnApplicationInTerm(n.Open, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationInTerm(n.Body, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationInTerm(n.Close, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarSeqTerm:
		p.validateGrammarFnApplicationsInTerms(n.Terms, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarLookaheadTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarConcatTerm:
		p.validateGrammarFnApplicationsInTerms(n.Terms, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarListTerm:
		p.validateGrammarFnApplicationInTerm(n.Elem, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationInTerm(n.Separator, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationsInTerms(n.Until, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarRepeatTerm:
		p.validateGrammarFnApplicationInTerm(n.Elem, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationsInTerms(n.Until, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarFlatRepeatTerm:
		p.validateGrammarFnApplicationInTerm(n.Elem, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationsInTerms(n.Until, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarSeparatedTerm:
		p.validateGrammarFnApplicationInTerm(n.Elem, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationInTerm(n.Separator, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationsInTerms(n.Until, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarSuffixTerm:
		p.validateGrammarFnApplicationInTerm(n.Seed, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationsInPostfixArms(n.Arms, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarPostfixTerm:
		p.validateGrammarFnApplicationInTerm(n.Seed, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationsInPostfixArms(n.Arms, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarPrecedenceTerm:
		p.validateGrammarFnApplicationInTerm(n.Seed, grammarFns, aliases, tokenSetNames)
		for _, level := range n.Levels {
			p.validateGrammarFnApplicationInTerm(level.Seed, grammarFns, aliases, tokenSetNames)
			p.validateGrammarFnApplicationsInPrecedenceArms(level.Arms, grammarFns, aliases, tokenSetNames)
		}
		p.validateGrammarFnApplicationsInPrecedenceArms(n.Arms, grammarFns, aliases, tokenSetNames)
	}
}

type grammarFnResolvedArg struct {
	Position lexer.Pos
	Term     ast.GrammarTerm
	Expr     ast.Expr
}

func (p *Parser) resolveGrammarFnApplyArgs(term *ast.GrammarApplyTerm, grammarFn ast.GrammarFnDecl) ([]grammarFnResolvedArg, bool) {
	return p.resolveGrammarApplyArgs(term, grammarFn.Params, "grammarfn")
}
func (p *Parser) resolveGrammarAliasApplyArgs(term *ast.GrammarApplyTerm, alias ast.GrammarAliasDecl) ([]grammarFnResolvedArg, bool) {
	return p.resolveGrammarApplyArgs(term, alias.Params, "grammar alias")
}
func (p *Parser) resolveGrammarApplyArgs(term *ast.GrammarApplyTerm, params []ast.GrammarFnParam, kind string) ([]grammarFnResolvedArg, bool) {
	resolved := make([]grammarFnResolvedArg, len(params))
	filled := make([]bool, len(params))
	paramIndex := make(map[string]int, len(params))
	for index, param := range params {
		paramIndex[param.Name] = index
	}

	ok := true
	nextPositional := 0
	seenNamed := false
	for _, arg := range term.Args {
		if arg.Name != "" {
			seenNamed = true
			index, found := paramIndex[arg.Name]
			if !found {
				p.errorAt(arg.Position, "unknown argument %q for %s %s", arg.Name, kind, term.Name)
				ok = false
				continue
			}
			if filled[index] {
				p.errorAt(arg.Position, "duplicate argument %q for %s %s", arg.Name, kind, term.Name)
				ok = false
				continue
			}
			resolved[index] = grammarFnResolvedArg{Position: arg.Position, Term: arg.Term, Expr: grammarFnExprArg(arg.Term)}
			filled[index] = true
			continue
		}
		if seenNamed {
			p.errorAt(arg.Position, "positional argument cannot follow named argument in %s %s", kind, term.Name)
			ok = false
			continue
		}
		for nextPositional < len(filled) && filled[nextPositional] {
			nextPositional++
		}
		if nextPositional >= len(params) {
			p.errorAt(arg.Position, "too many positional arguments for %s %s", kind, term.Name)
			ok = false
			continue
		}
		resolved[nextPositional] = grammarFnResolvedArg{Position: arg.Position, Term: arg.Term, Expr: grammarFnExprArg(arg.Term)}
		filled[nextPositional] = true
		nextPositional++
	}

	for index, param := range params {
		if filled[index] {
			continue
		}
		if param.Default != nil {
			resolved[index] = grammarFnResolvedArg{Position: param.Default.Pos(), Term: param.Default, Expr: grammarFnExprArg(param.Default)}
			filled[index] = true
			continue
		}
		if param.DefaultExpr != nil {
			resolved[index] = grammarFnResolvedArg{Position: param.DefaultExpr.Pos(), Expr: param.DefaultExpr}
			filled[index] = true
			continue
		}
		p.errorAt(term.Position, "missing argument %q for %s %s", param.Name, kind, term.Name)
		ok = false
	}
	return resolved, ok
}
func (p *Parser) validateGrammarApplyArgTypes(kind string, name string, params []ast.GrammarFnParam, resolved []grammarFnResolvedArg, tokenSetNames map[string]bool) {
	for index, arg := range resolved {
		param := params[index]
		if param.Type.Kind == "" {
			continue
		}
		argKind := grammarApplyArgKind(arg, tokenSetNames)
		if param.Type.Kind == "tokenset" && argKind != "tokenset" {
			if _, ok := arg.Term.(*ast.GrammarTokenSetRefTerm); ok {
				continue
			}
			p.errorAt(arg.Position, "%s %s argument %q expects tokenset, got %s", kind, name, param.Name, argKind)
		}
		if param.Type.Kind == "grammar" && argKind == "tokenset" {
			p.errorAt(arg.Position, "%s %s argument %q expects grammar, got tokenset", kind, name, param.Name)
		}
		if param.Type.Kind == "expr" && argKind != "expr" {
			p.errorAt(arg.Position, "%s %s argument %q expects expr, got %s", kind, name, param.Name, argKind)
		}
	}
}
func (p *Parser) validateGrammarFnApplicationsInPostfixArms(arms []ast.GrammarPostfixArm, grammarFns map[string]ast.GrammarFnDecl, aliases map[string]ast.GrammarAliasDecl, tokenSetNames map[string]bool) {
	for _, arm := range arms {
		p.validateGrammarFnApplicationInTerm(arm.Op, grammarFns, aliases, tokenSetNames)
		for _, binding := range arm.Bindings {
			if binding != nil {
				p.validateGrammarFnApplicationInTerm(binding.Term, grammarFns, aliases, tokenSetNames)
			}
		}
	}
}
func (p *Parser) validateGrammarFnApplicationsInPrecedenceArms(arms []ast.GrammarPrecedenceArm, grammarFns map[string]ast.GrammarFnDecl, aliases map[string]ast.GrammarAliasDecl, tokenSetNames map[string]bool) {
	for _, arm := range arms {
		p.validateGrammarFnApplicationInTerm(arm.Op, grammarFns, aliases, tokenSetNames)
		for _, binding := range arm.Bindings {
			if binding != nil {
				p.validateGrammarFnApplicationInTerm(binding.Term, grammarFns, aliases, tokenSetNames)
			}
		}
	}
}
func grammarApplyArgKind(arg grammarFnResolvedArg, tokenSetNames map[string]bool) string {
	if arg.Expr != nil {
		return "expr"
	}
	return grammarFnArgKind(arg.Term, tokenSetNames)
}
func grammarFnArgKind(term ast.GrammarTerm, tokenSetNames map[string]bool) string {
	if _, ok := term.(*ast.GrammarExprTerm); ok {
		return "expr"
	}
	if call, ok := term.(*ast.GrammarCallTerm); ok && !call.Explicit && len(call.Args) == 0 {
		return "expr"
	}
	if ref, ok := term.(*ast.GrammarTokenSetRefTerm); ok && tokenSetNames[ref.Name] {
		return "tokenset"
	}
	if _, ok := term.(*ast.GrammarTokenSetRefTerm); ok {
		return "expr"
	}
	return "grammar"
}
func grammarFnExprArg(term ast.GrammarTerm) ast.Expr {
	if exprTerm, ok := term.(*ast.GrammarExprTerm); ok {
		return exprTerm.Expr
	}
	return nil
}
func (p *Parser) validateGrammarProductionBodies(grammarFns []ast.GrammarFnDecl, productions []ast.GrammarProductionDecl) {
	for _, production := range productions {
		p.validateGrammarProductionTermSequence(production.Name, production.Terms)
	}
}
func (p *Parser) validateGrammarProductionTermSequence(productionName string, terms []ast.GrammarTerm) {
	for _, term := range terms {
		p.validateGrammarProductionTerm(productionName, term)
	}
}
