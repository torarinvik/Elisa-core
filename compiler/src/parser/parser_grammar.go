package parser

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (p *Parser) parseGrammarDecl() *ast.GrammarDecl {
	pos := p.cur().Pos
	extend := p.peekIdentText("extend")
	if extend {
		p.expectIdentText("extend")
	}
	p.expectIdentText("grammar")
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeParams, refStorageParams, refStateParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()
	var envType ast.TypeExpr
	var overType ast.TypeExpr
	var usingType ast.TypeExpr
	uses := make([]ast.TypeExpr, 0)
	if p.peek() == lexer.TOKEN_WITH {
		p.expect(lexer.TOKEN_WITH)
		envType = p.parseGrammarHeaderTypeExpr()
	}
	if p.peekIdentText("over") {
		p.expectIdentText("over")
		overType = p.parseGrammarHeaderTypeExpr()
	}
	if p.peekIdentText("using") {
		p.expectIdentText("using")
		usingType = p.parseGrammarHeaderTypeExpr()
	}
	if p.peekIdentText("uses") {
		p.expectIdentText("uses")
		for {
			uses = append(uses, p.parseGrammarHeaderTypeExpr())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	var errorType ast.TypeExpr
	var cursorExpr ast.Expr
	var allocExpr ast.Expr
	var tokenKindType ast.TypeExpr
	var eofExpr ast.Expr
	var tokenKindField string
	var currentFunc string
	var advanceFunc string
	var expectFunc string
	var expectKindFunc string
	var recordErrorFunc string
	tokenAliases := make([]ast.GrammarTokenAliasDecl, 0)
	channels := make([]ast.GrammarChannelDecl, 0)
	tokenSets := make([]ast.GrammarTokenSetDecl, 0)
	grammarFns := make([]ast.GrammarFnDecl, 0)
	recoveryPolicies := make([]ast.GrammarRecoveryDecl, 0)
	infixTables := make([]ast.GrammarInfixTableDecl, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if !p.peekGrammarHeaderDecl() {
			break
		}
		switch {
		case p.peek() == lexer.TOKEN_ERROR:
			errorType = p.parseGrammarErrorHeaderDecl()
		case p.peekIdentText("cursor"):
			cursorExpr = p.parseGrammarValueHeaderDecl("cursor")
		case p.peekIdentText("alloc"):
			allocExpr = p.parseGrammarValueHeaderDecl("alloc")
		case p.peekIdentText("token_kind"):
			tokenKindType = p.parseGrammarTypeHeaderDecl("token_kind")
		case p.peekIdentText("eof"):
			eofExpr = p.parseGrammarValueHeaderDecl("eof")
		case p.peekIdentText("token_field"):
			tokenKindField = p.parseGrammarNameHeaderDecl("token_field")
		case p.peekIdentText("current"):
			currentFunc = p.parseGrammarNameHeaderDecl("current")
		case p.peekIdentText("advance"):
			advanceFunc = p.parseGrammarNameHeaderDecl("advance")
		case p.peekIdentText("expect"):
			expectFunc = p.parseGrammarNameHeaderDecl("expect")
		case p.peekIdentText("expect_kind"):
			expectKindFunc = p.parseGrammarNameHeaderDecl("expect_kind")
		case p.peekIdentText("record_error"):
			recordErrorFunc = p.parseGrammarNameHeaderDecl("record_error")
		case p.peekIdentText("token"):
			tokenAliases = append(tokenAliases, p.parseGrammarTokenAliasDecls()...)
		case p.peekIdentText("channel"):
			channels = append(channels, p.parseGrammarChannelDecl())
		case p.peekIdentText("tokenset"):
			tokenSets = append(tokenSets, p.parseGrammarTokenSetDecl())
		case p.peekIdentText("grammarfn"):
			grammarFns = append(grammarFns, p.parseGrammarFnDecl())
		case p.peekIdentText("recovery"):
			recoveryPolicies = append(recoveryPolicies, p.parseGrammarRecoveryDecl())
		case p.peekGrammarInfixTableDecl():
			infixTables = append(infixTables, p.parseGrammarInfixTableDecl())
		default:
			break
		}
	}

	productions := make([]ast.GrammarProductionDecl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		productions = append(productions, p.parseGrammarProductionDecls()...)
	}
	p.expect(lexer.TOKEN_DEDENT)
	tokenSets = normalizeGrammarTokenSetItemNames(tokenSets)
	p.validateGrammarFnApplications(grammarFns, tokenSets, productions)

	return &ast.GrammarDecl{
		Position:         pos,
		Extend:           extend,
		Name:             name,
		TypeParams:       typeParams,
		RefStorageParams: refStorageParams,
		RefStateParams:   refStateParams,
		RegionParams:     regionParams,
		PermissionParams: permissionParams,
		GenericParams:    genericParams,
		EnvType:          envType,
		OverType:         overType,
		UsingType:        usingType,
		Uses:             uses,
		ErrorType:        errorType,
		CursorExpr:       cursorExpr,
		AllocExpr:        allocExpr,
		TokenKindType:    tokenKindType,
		EOFExpr:          eofExpr,
		TokenKindField:   tokenKindField,
		CurrentFunc:      currentFunc,
		AdvanceFunc:      advanceFunc,
		ExpectFunc:       expectFunc,
		ExpectKindFunc:   expectKindFunc,
		RecordErrorFunc:  recordErrorFunc,
		TokenAliases:     tokenAliases,
		Channels:         channels,
		TokenSets:        tokenSets,
		GrammarFns:       grammarFns,
		RecoveryPolicies: recoveryPolicies,
		InfixTables:      infixTables,
		Productions:      productions,
	}
}

func (p *Parser) peekGrammarInfixTableDecl() bool {
	if !p.peekIdentText("infix") {
		return false
	}
	if p.pos+1 >= len(p.tokens) {
		return false
	}
	next := p.tokens[p.pos+1]
	return next.Kind == lexer.TOKEN_IDENT && next.Text == "table"
}

func (p *Parser) parseGrammarEnvDecl() *ast.GrammarEnvDecl {
	pos := p.cur().Pos
	p.expectIdentText("grammarenv")
	name := p.expect(lexer.TOKEN_IDENT).Text
	var overType ast.TypeExpr
	var usingType ast.TypeExpr
	if p.peekIdentText("over") {
		p.expectIdentText("over")
		overType = p.parseGrammarHeaderTypeExpr()
	}
	if p.peekIdentText("using") {
		p.expectIdentText("using")
		usingType = p.parseGrammarHeaderTypeExpr()
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	var errorType ast.TypeExpr
	var cursorExpr ast.Expr
	var allocExpr ast.Expr
	var tokenKindType ast.TypeExpr
	var eofExpr ast.Expr
	var tokenKindField string
	var currentFunc string
	var advanceFunc string
	var expectFunc string
	var expectKindFunc string
	var recordErrorFunc string
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		switch {
		case p.peek() == lexer.TOKEN_ERROR:
			errorType = p.parseGrammarErrorHeaderDecl()
		case p.peekIdentText("cursor"):
			cursorExpr = p.parseGrammarValueHeaderDecl("cursor")
		case p.peekIdentText("alloc"):
			allocExpr = p.parseGrammarValueHeaderDecl("alloc")
		case p.peekIdentText("token_kind"):
			tokenKindType = p.parseGrammarTypeHeaderDecl("token_kind")
		case p.peekIdentText("eof"):
			eofExpr = p.parseGrammarValueHeaderDecl("eof")
		case p.peekIdentText("token_field"):
			tokenKindField = p.parseGrammarNameHeaderDecl("token_field")
		case p.peekIdentText("current"):
			currentFunc = p.parseGrammarNameHeaderDecl("current")
		case p.peekIdentText("advance"):
			advanceFunc = p.parseGrammarNameHeaderDecl("advance")
		case p.peekIdentText("expect"):
			expectFunc = p.parseGrammarNameHeaderDecl("expect")
		case p.peekIdentText("expect_kind"):
			expectKindFunc = p.parseGrammarNameHeaderDecl("expect_kind")
		case p.peekIdentText("record_error"):
			recordErrorFunc = p.parseGrammarNameHeaderDecl("record_error")
		default:
			p.errorf("expected grammarenv header declaration")
			p.advance()
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return &ast.GrammarEnvDecl{
		Position:        pos,
		Name:            name,
		OverType:        overType,
		UsingType:       usingType,
		ErrorType:       errorType,
		CursorExpr:      cursorExpr,
		AllocExpr:       allocExpr,
		TokenKindType:   tokenKindType,
		EOFExpr:         eofExpr,
		TokenKindField:  tokenKindField,
		CurrentFunc:     currentFunc,
		AdvanceFunc:     advanceFunc,
		ExpectFunc:      expectFunc,
		ExpectKindFunc:  expectKindFunc,
		RecordErrorFunc: recordErrorFunc,
	}
}

func (p *Parser) peekGrammarHeaderDecl() bool {
	if p.peek() == lexer.TOKEN_ERROR {
		return true
	}
	if p.peekGrammarInfixTableDecl() {
		return true
	}
	if p.peek() != lexer.TOKEN_IDENT {
		return false
	}
	if p.pos+1 >= len(p.tokens) {
		return false
	}
	next := p.tokens[p.pos+1].Kind
	switch p.cur().Text {
	case "cursor", "alloc", "token_kind", "eof", "token_field", "current", "advance", "expect", "expect_kind", "record_error", "token", "channel":
		return next != lexer.TOKEN_LPAREN
	case "tokenset":
		return next == lexer.TOKEN_IDENT
	case "grammarfn":
		return next == lexer.TOKEN_IDENT
	case "recovery":
		return next == lexer.TOKEN_IDENT
	default:
		return false
	}
}

func (p *Parser) parseGrammarErrorHeaderDecl() ast.TypeExpr {
	p.expect(lexer.TOKEN_ERROR)
	typeExpr := p.parseGrammarHeaderTypeExpr()
	p.expectNewline()
	return typeExpr
}

func (p *Parser) parseGrammarHeaderTypeExpr() ast.TypeExpr {
	pos := p.cur().Pos
	base := p.parseBaseType(ast.RefStorageAny, false, "", "", "")
	if base == nil {
		p.errorf("expected grammar header type")
		return &ast.NamedType{Position: pos, Name: "<invalid>"}
	}
	return base
}

func (p *Parser) parseGrammarValueHeaderDecl(keyword string) ast.Expr {
	p.expectIdentText(keyword)
	value := p.parseExpr()
	p.expectNewline()
	return value
}

func (p *Parser) parseGrammarTypeHeaderDecl(keyword string) ast.TypeExpr {
	p.expectIdentText(keyword)
	value := p.parseGrammarHeaderTypeExpr()
	p.expectNewline()
	return value
}

func (p *Parser) parseGrammarNameHeaderDecl(keyword string) string {
	p.expectIdentText(keyword)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return name
}

func (p *Parser) parseGrammarTokenAliasDecls() []ast.GrammarTokenAliasDecl {
	pos := p.cur().Pos
	p.expectIdentText("token")
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		p.expect(lexer.TOKEN_INDENT)
		aliases := make([]ast.GrammarTokenAliasDecl, 0, p.estimateIndentedItemCount())
		for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
			p.skipNewlines()
			if p.peek() == lexer.TOKEN_DEDENT {
				break
			}
			aliases = append(aliases, p.parseGrammarTokenAliasBlockItem())
		}
		p.expect(lexer.TOKEN_DEDENT)
		return aliases
	}
	return []ast.GrammarTokenAliasDecl{p.parseGrammarTokenAliasLine(pos)}
}

func (p *Parser) parseGrammarTokenAliasLine(pos lexer.Pos) ast.GrammarTokenAliasDecl {
	term := p.parseGrammarTokenKindTerm()
	alias := p.finishGrammarTokenAlias(pos, term.Kind)
	p.expectNewline()
	return alias
}

func (p *Parser) parseGrammarTokenAliasBlockItem() ast.GrammarTokenAliasDecl {
	pos := p.cur().Pos
	var kind string
	if p.peek() == lexer.TOKEN_DOT {
		kind = p.parseGrammarTokenKindTerm().Kind
	} else {
		kind = p.expect(lexer.TOKEN_IDENT).Text
	}
	alias := p.finishGrammarTokenAlias(pos, kind)
	p.expectNewline()
	return alias
}

func (p *Parser) finishGrammarTokenAlias(pos lexer.Pos, kind string) ast.GrammarTokenAliasDecl {
	alias := ast.GrammarTokenAliasDecl{Position: pos, Kind: kind}
	if p.peek() == lexer.TOKEN_STRING_LIT {
		alias.Literal = p.advance().Text
		alias.HasLiteral = true
	}
	return alias
}

func (p *Parser) parseGrammarChannelDecl() ast.GrammarChannelDecl {
	pos := p.cur().Pos
	p.expectIdentText("channel")
	name := p.expect(lexer.TOKEN_IDENT).Text
	var typeExpr ast.TypeExpr
	var defaultExpr ast.Expr
	if p.match(lexer.TOKEN_COLON) {
		typeExpr = p.parseGrammarHeaderTypeExpr()
	}
	if p.match(lexer.TOKEN_ASSIGN) {
		defaultExpr = p.parseExpr()
	}
	p.expectNewline()
	return ast.GrammarChannelDecl{Position: pos, Name: name, Type: typeExpr, Default: defaultExpr}
}

func (p *Parser) parseGrammarTokenSetDecl() ast.GrammarTokenSetDecl {
	pos := p.cur().Pos
	p.expectIdentText("tokenset")
	name := p.expect(lexer.TOKEN_IDENT).Text
	terms := make([]ast.GrammarTerm, 0, 4)
	if p.match(lexer.TOKEN_ASSIGN) {
		for {
			terms = append(terms, p.parseGrammarTokenSetItem())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expectNewline()
		return ast.GrammarTokenSetDecl{Position: pos, Name: name, Terms: terms}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		terms = append(terms, p.parseGrammarTokenSetItem())
		p.expectNewline()
	}
	p.expect(lexer.TOKEN_DEDENT)
	return ast.GrammarTokenSetDecl{Position: pos, Name: name, Terms: terms}
}

func (p *Parser) parseGrammarTokenSetItem() ast.GrammarTerm {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind != lexer.TOKEN_LPAREN {
		return &ast.GrammarTokenSetRefTerm{Position: pos, Name: p.advance().Text}
	}
	return p.parseGrammarRecoverableTermValue()
}

func normalizeGrammarTokenSetItemNames(tokenSets []ast.GrammarTokenSetDecl) []ast.GrammarTokenSetDecl {
	if len(tokenSets) == 0 {
		return nil
	}
	setNames := make(map[string]bool, len(tokenSets))
	for _, tokenSet := range tokenSets {
		if tokenSet.Name != "" {
			setNames[tokenSet.Name] = true
		}
	}
	normalized := make([]ast.GrammarTokenSetDecl, 0, len(tokenSets))
	for _, tokenSet := range tokenSets {
		terms := make([]ast.GrammarTerm, 0, len(tokenSet.Terms))
		for _, term := range tokenSet.Terms {
			ref, ok := term.(*ast.GrammarTokenSetRefTerm)
			if !ok || setNames[ref.Name] {
				terms = append(terms, term)
				continue
			}
			terms = append(terms, &ast.GrammarTokenKindTerm{Position: ref.Position, Kind: ref.Name})
		}
		tokenSet.Terms = terms
		normalized = append(normalized, tokenSet)
	}
	return normalized
}

func (p *Parser) parseGrammarFnDecl() ast.GrammarFnDecl {
	pos := p.cur().Pos
	p.expectIdentText("grammarfn")
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeParams, _, _, _, _, genericParams := p.parseFuncGenericParams()
	p.expect(lexer.TOKEN_LPAREN)
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
	var ret ast.GrammarFnType
	if p.match(lexer.TOKEN_ARROW) {
		ret = p.parseGrammarFnType()
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	terms := p.parseGrammarTermBlock()
	return ast.GrammarFnDecl{Position: pos, Name: name, TypeParams: typeParams, GenericParams: genericParams, Params: params, Return: ret, Terms: terms}
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

func (p *Parser) validateGrammarFnApplications(grammarFns []ast.GrammarFnDecl, tokenSets []ast.GrammarTokenSetDecl, productions []ast.GrammarProductionDecl) {
	if len(grammarFns) == 0 {
		return
	}
	fnMap := make(map[string]ast.GrammarFnDecl, len(grammarFns))
	for _, grammarFn := range grammarFns {
		if grammarFn.Name != "" {
			fnMap[grammarFn.Name] = grammarFn
		}
	}
	tokenSetNames := make(map[string]bool, len(tokenSets))
	for _, tokenSet := range tokenSets {
		if tokenSet.Name != "" {
			tokenSetNames[tokenSet.Name] = true
		}
	}
	for _, grammarFn := range grammarFns {
		localTokenSetNames := tokenSetNames
		copiedTokenSetNames := false
		for _, param := range grammarFn.Params {
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
		for _, param := range grammarFn.Params {
			if param.Default != nil {
				p.validateGrammarFnApplicationInTerm(param.Default, fnMap, localTokenSetNames)
			}
		}
		p.validateGrammarFnApplicationsInTerms(grammarFn.Terms, fnMap, localTokenSetNames)
	}
	for _, production := range productions {
		p.validateGrammarFnApplicationsInTerms(production.Terms, fnMap, tokenSetNames)
		p.validateGrammarFnApplicationsInTerms(production.RecoverUntil, fnMap, tokenSetNames)
	}
}

func (p *Parser) validateGrammarFnApplicationsInTerms(terms []ast.GrammarTerm, grammarFns map[string]ast.GrammarFnDecl, tokenSetNames map[string]bool) {
	for _, term := range terms {
		p.validateGrammarFnApplicationInTerm(term, grammarFns, tokenSetNames)
	}
}

func (p *Parser) validateGrammarFnApplicationInTerm(term ast.GrammarTerm, grammarFns map[string]ast.GrammarFnDecl, tokenSetNames map[string]bool) {
	switch n := term.(type) {
	case *ast.GrammarApplyTerm:
		grammarFn, ok := grammarFns[n.Name]
		if !ok {
			p.errorAt(n.Position, "unknown grammar function %q", n.Name)
		} else {
			resolved, ok := p.resolveGrammarFnApplyArgs(n, grammarFn)
			if ok {
				for index, arg := range resolved {
					param := grammarFn.Params[index]
					if param.Type.Kind == "" {
						continue
					}
					argKind := grammarFnArgKind(arg.Term, tokenSetNames)
					if param.Type.Kind == "tokenset" && argKind != "tokenset" {
						p.errorAt(arg.Position, "grammarfn %s argument %q expects tokenset, got %s", n.Name, param.Name, argKind)
					}
					if param.Type.Kind == "grammar" && argKind == "tokenset" {
						p.errorAt(arg.Position, "grammarfn %s argument %q expects grammar, got tokenset", n.Name, param.Name)
					}
					if param.Type.Kind == "expr" && argKind != "expr" {
						p.errorAt(arg.Position, "grammarfn %s argument %q expects expr, got %s", n.Name, param.Name, argKind)
					}
				}
			}
		}
		for _, arg := range n.Args {
			p.validateGrammarFnApplicationInTerm(arg.Term, grammarFns, tokenSetNames)
		}
	case *ast.GrammarBindTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, tokenSetNames)
	case *ast.GrammarAssignTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, tokenSetNames)
	case *ast.GrammarChoiceTerm:
		p.validateGrammarFnApplicationsInTerms(n.Options, grammarFns, tokenSetNames)
	case *ast.GrammarOptionalTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, tokenSetNames)
	case *ast.GrammarWhenTerm:
		p.validateGrammarFnApplicationInTerm(n.Then, grammarFns, tokenSetNames)
		p.validateGrammarFnApplicationInTerm(n.Else, grammarFns, tokenSetNames)
	case *ast.GrammarRecoverTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, tokenSetNames)
		p.validateGrammarFnApplicationsInTerms(n.RecoverUntil, grammarFns, tokenSetNames)
	case *ast.GrammarRequiredTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, tokenSetNames)
	case *ast.GrammarDelimitedTerm:
		p.validateGrammarFnApplicationInTerm(n.Open, grammarFns, tokenSetNames)
		p.validateGrammarFnApplicationInTerm(n.Body, grammarFns, tokenSetNames)
		p.validateGrammarFnApplicationInTerm(n.Close, grammarFns, tokenSetNames)
	case *ast.GrammarSeqTerm:
		p.validateGrammarFnApplicationsInTerms(n.Terms, grammarFns, tokenSetNames)
	case *ast.GrammarLookaheadTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, tokenSetNames)
	case *ast.GrammarConcatTerm:
		p.validateGrammarFnApplicationsInTerms(n.Terms, grammarFns, tokenSetNames)
	case *ast.GrammarListTerm:
		p.validateGrammarFnApplicationInTerm(n.Elem, grammarFns, tokenSetNames)
		p.validateGrammarFnApplicationInTerm(n.Separator, grammarFns, tokenSetNames)
		p.validateGrammarFnApplicationsInTerms(n.Until, grammarFns, tokenSetNames)
	case *ast.GrammarRepeatTerm:
		p.validateGrammarFnApplicationInTerm(n.Elem, grammarFns, tokenSetNames)
		p.validateGrammarFnApplicationsInTerms(n.Until, grammarFns, tokenSetNames)
	case *ast.GrammarFlatRepeatTerm:
		p.validateGrammarFnApplicationInTerm(n.Elem, grammarFns, tokenSetNames)
		p.validateGrammarFnApplicationsInTerms(n.Until, grammarFns, tokenSetNames)
	case *ast.GrammarSeparatedTerm:
		p.validateGrammarFnApplicationInTerm(n.Elem, grammarFns, tokenSetNames)
		p.validateGrammarFnApplicationInTerm(n.Separator, grammarFns, tokenSetNames)
		p.validateGrammarFnApplicationsInTerms(n.Until, grammarFns, tokenSetNames)
	case *ast.GrammarSuffixTerm:
		p.validateGrammarFnApplicationInTerm(n.Seed, grammarFns, tokenSetNames)
		p.validateGrammarFnApplicationsInPostfixArms(n.Arms, grammarFns, tokenSetNames)
	case *ast.GrammarPostfixTerm:
		p.validateGrammarFnApplicationInTerm(n.Seed, grammarFns, tokenSetNames)
		p.validateGrammarFnApplicationsInPostfixArms(n.Arms, grammarFns, tokenSetNames)
	case *ast.GrammarPrecedenceTerm:
		p.validateGrammarFnApplicationInTerm(n.Seed, grammarFns, tokenSetNames)
		for _, level := range n.Levels {
			p.validateGrammarFnApplicationInTerm(level.Seed, grammarFns, tokenSetNames)
			p.validateGrammarFnApplicationsInPrecedenceArms(level.Arms, grammarFns, tokenSetNames)
		}
		p.validateGrammarFnApplicationsInPrecedenceArms(n.Arms, grammarFns, tokenSetNames)
	}
}

type grammarFnResolvedArg struct {
	Position lexer.Pos
	Term     ast.GrammarTerm
	Expr     ast.Expr
}

func (p *Parser) resolveGrammarFnApplyArgs(term *ast.GrammarApplyTerm, grammarFn ast.GrammarFnDecl) ([]grammarFnResolvedArg, bool) {
	resolved := make([]grammarFnResolvedArg, len(grammarFn.Params))
	filled := make([]bool, len(grammarFn.Params))
	paramIndex := make(map[string]int, len(grammarFn.Params))
	for index, param := range grammarFn.Params {
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
				p.errorAt(arg.Position, "unknown argument %q for grammarfn %s", arg.Name, term.Name)
				ok = false
				continue
			}
			if filled[index] {
				p.errorAt(arg.Position, "duplicate argument %q for grammarfn %s", arg.Name, term.Name)
				ok = false
				continue
			}
			resolved[index] = grammarFnResolvedArg{Position: arg.Position, Term: arg.Term, Expr: grammarFnExprArg(arg.Term)}
			filled[index] = true
			continue
		}
		if seenNamed {
			p.errorAt(arg.Position, "positional argument cannot follow named argument in grammarfn %s", term.Name)
			ok = false
			continue
		}
		for nextPositional < len(filled) && filled[nextPositional] {
			nextPositional++
		}
		if nextPositional >= len(grammarFn.Params) {
			p.errorAt(arg.Position, "too many positional arguments for grammarfn %s", term.Name)
			ok = false
			continue
		}
		resolved[nextPositional] = grammarFnResolvedArg{Position: arg.Position, Term: arg.Term, Expr: grammarFnExprArg(arg.Term)}
		filled[nextPositional] = true
		nextPositional++
	}

	for index, param := range grammarFn.Params {
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
		p.errorAt(term.Position, "missing argument %q for grammarfn %s", param.Name, term.Name)
		ok = false
	}
	return resolved, ok
}

func (p *Parser) validateGrammarFnApplicationsInPostfixArms(arms []ast.GrammarPostfixArm, grammarFns map[string]ast.GrammarFnDecl, tokenSetNames map[string]bool) {
	for _, arm := range arms {
		p.validateGrammarFnApplicationInTerm(arm.Op, grammarFns, tokenSetNames)
		for _, binding := range arm.Bindings {
			if binding != nil {
				p.validateGrammarFnApplicationInTerm(binding.Term, grammarFns, tokenSetNames)
			}
		}
	}
}

func (p *Parser) validateGrammarFnApplicationsInPrecedenceArms(arms []ast.GrammarPrecedenceArm, grammarFns map[string]ast.GrammarFnDecl, tokenSetNames map[string]bool) {
	for _, arm := range arms {
		p.validateGrammarFnApplicationInTerm(arm.Op, grammarFns, tokenSetNames)
		for _, binding := range arm.Bindings {
			if binding != nil {
				p.validateGrammarFnApplicationInTerm(binding.Term, grammarFns, tokenSetNames)
			}
		}
	}
}

func grammarFnArgKind(term ast.GrammarTerm, tokenSetNames map[string]bool) string {
	if _, ok := term.(*ast.GrammarExprTerm); ok {
		return "expr"
	}
	if ref, ok := term.(*ast.GrammarTokenSetRefTerm); ok && tokenSetNames[ref.Name] {
		return "tokenset"
	}
	return "grammar"
}

func grammarFnExprArg(term ast.GrammarTerm) ast.Expr {
	if exprTerm, ok := term.(*ast.GrammarExprTerm); ok {
		return exprTerm.Expr
	}
	return nil
}

func (p *Parser) parseGrammarRecoveryDecl() ast.GrammarRecoveryDecl {
	pos := p.cur().Pos
	p.expectIdentText("recovery")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	decl := ast.GrammarRecoveryDecl{Position: pos, Name: name}
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		switch {
		case p.peekIdentText("message"):
			p.expectIdentText("message")
			decl.Message = p.parseExpr()
			p.expectNewline()
		case p.peekIdentText("until"):
			decl.Until = p.parseGrammarRecoveryUntilDecl()
		case p.peekIdentText("fallback"):
			p.expectIdentText("fallback")
			decl.Fallback = p.parseExpr()
			p.expectNewline()
		default:
			p.errorf("expected grammar recovery declaration item")
			p.advance()
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	if decl.Message == nil {
		p.errorf("expected recovery declaration message")
	}
	if len(decl.Until) == 0 {
		p.errorf("expected recovery declaration until clause")
	}
	return decl
}

func (p *Parser) parseGrammarInfixTableDecl() ast.GrammarInfixTableDecl {
	pos := p.cur().Pos
	p.expectIdentText("infix")
	p.expectIdentText("table")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_LPAREN)
	result := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_RPAREN)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	levels := make([]ast.GrammarPrecedenceLevel, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		levels = append(levels, p.parseGrammarPrecedenceLevel())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return ast.GrammarInfixTableDecl{Position: pos, Name: name, Result: result, Levels: levels}
}

func (p *Parser) parseGrammarRecoveryUntilDecl() []ast.GrammarTerm {
	p.expectIdentText("until")
	if p.peek() == lexer.TOKEN_LPAREN {
		terms := p.parseGrammarUntilClauseBody()
		p.expectNewline()
		return terms
	}
	terms := make([]ast.GrammarTerm, 0, 4)
	for {
		terms = append(terms, p.parseGrammarUntilStopTerm())
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expectNewline()
	return terms
}

func (p *Parser) parseGrammarProductionDecls() []ast.GrammarProductionDecl {
	pos := p.cur().Pos
	public := p.peekIdentText("pub") && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT
	if public {
		p.expectIdentText("pub")
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	if p.match(lexer.TOKEN_PLUSEQ) {
		if p.match(lexer.TOKEN_COLON) {
			p.expectNewline()
			groupedTerms := p.parseGroupedGrammarAppendTermBlocks()
			productions := make([]ast.GrammarProductionDecl, 0, len(groupedTerms))
			for _, terms := range groupedTerms {
				productions = append(productions, ast.GrammarProductionDecl{Position: pos, Public: public, Append: true, Name: name, Terms: terms})
			}
			return productions
		}
		p.expectNewline()
		terms := p.parseGrammarTermBlock()
		return []ast.GrammarProductionDecl{{Position: pos, Public: public, Append: true, Name: name, Terms: terms}}
	}
	hasParamList := p.match(lexer.TOKEN_LPAREN)
	params := make([]ast.ParamDecl, 0)
	if hasParamList {
		params = p.parseParamList(false)
		p.expect(lexer.TOKEN_RPAREN)
	}

	var retType ast.TypeExpr
	var recoverPolicy string
	var recoverMsg ast.Expr
	var recoverUntil []ast.GrammarTerm
	var recoverValue ast.Expr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}
	if p.peekIdentText("recover") {
		recoverPolicy, recoverMsg, recoverUntil, recoverValue = p.parseGrammarRecoverSpec()
	}

	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	terms := p.parseGrammarTermBlock()
	return []ast.GrammarProductionDecl{{Position: pos, Public: public, Name: name, HasParamList: hasParamList, Params: params, ReturnType: retType, RecoverPolicy: recoverPolicy, RecoverMsg: recoverMsg, RecoverUntil: recoverUntil, RecoverValue: recoverValue, Terms: terms}}
}

func (p *Parser) parseGroupedGrammarAppendTermBlocks() [][]ast.GrammarTerm {
	p.expect(lexer.TOKEN_INDENT)
	groups := make([][]ast.GrammarTerm, 0, p.estimateIndentedItemCount())
	current := make([]ast.GrammarTerm, 0, p.estimateIndentedItemCount())
	lastWasSeparator := false
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		for p.peek() == lexer.TOKEN_NEWLINE {
			p.advance()
		}
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() == lexer.TOKEN_PIPE {
			if len(current) == 0 {
				p.errorf("expected grouped append arm before '|' separator")
			}
			p.advance()
			p.expectNewline()
			if len(current) != 0 {
				groups = append(groups, current)
				current = make([]ast.GrammarTerm, 0, p.estimateIndentedItemCount())
			}
			lastWasSeparator = true
			continue
		}
		term := p.parseGrammarTerm()
		if term != nil {
			current = append(current, term)
			lastWasSeparator = false
		}
	}
	if len(current) != 0 {
		groups = append(groups, current)
	} else if lastWasSeparator {
		p.errorf("expected grouped append arm after '|' separator")
	}
	p.expect(lexer.TOKEN_DEDENT)
	return groups
}

func (p *Parser) parseGrammarRecoverSpec() (string, ast.Expr, []ast.GrammarTerm, ast.Expr) {
	p.expectIdentText("recover")
	if p.peek() != lexer.TOKEN_LPAREN {
		return p.expect(lexer.TOKEN_IDENT).Text, nil, nil, nil
	}
	message, until, fallback := p.parseGrammarRecoverClauseBody()
	return "", message, until, fallback
}

func (p *Parser) parseGrammarRecoverClause() (ast.Expr, []ast.GrammarTerm, ast.Expr) {
	p.expectIdentText("recover")
	return p.parseGrammarRecoverClauseBody()
}

func (p *Parser) parseGrammarRecoverClauseBody() (ast.Expr, []ast.GrammarTerm, ast.Expr) {
	p.expect(lexer.TOKEN_LPAREN)
	message := p.parseExpr()
	p.expect(lexer.TOKEN_COMMA)
	until := p.parseGrammarUntilClause()
	var fallback ast.Expr
	if p.match(lexer.TOKEN_COMMA) {
		fallback = p.parseExpr()
	}
	p.expect(lexer.TOKEN_RPAREN)
	return message, until, fallback
}

func (p *Parser) wrapGrammarRecoverTerm(term ast.GrammarTerm) ast.GrammarTerm {
	if term == nil || !p.peekIdentText("recover") {
		return term
	}
	policy, message, until, fallback := p.parseGrammarRecoverSpec()
	return &ast.GrammarRecoverTerm{
		Position:      term.Pos(),
		Term:          term,
		RecoverPolicy: policy,
		RecoverMsg:    message,
		RecoverUntil:  until,
		RecoverValue:  fallback,
	}
}

func (p *Parser) parseGrammarTermBlock() []ast.GrammarTerm {
	p.expect(lexer.TOKEN_INDENT)
	terms := make([]ast.GrammarTerm, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		term := p.parseGrammarTerm()
		if term != nil {
			terms = append(terms, term)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return terms
}

func (p *Parser) parseGrammarTerm() ast.GrammarTerm {
	if p.peek() == lexer.TOKEN_PASS {
		pos := p.cur().Pos
		p.advance()
		p.expectNewline()
		return &ast.GrammarPassTerm{Position: pos}
	}
	if p.peek() == lexer.TOKEN_RETURN {
		pos := p.cur().Pos
		p.advance()
		value := p.parseExpr()
		p.expectNewline()
		return &ast.GrammarReturnTerm{Position: pos, Value: value}
	}
	if p.peekGrammarPrecedenceTerm() {
		return p.parseGrammarPrecedenceTerm()
	}
	if p.peekGrammarBlockTerm("suffix") {
		return p.parseGrammarSuffixTerm()
	}
	if p.peekGrammarBlockTerm("postfix") {
		return p.parseGrammarPostfixTerm()
	}
	if p.peekGrammarTokenKindBinding() {
		binding := p.parseGrammarTokenKindBinding()
		p.expectNewline()
		return binding
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LARROW {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_LARROW)
		term := p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
		if grammarTermNeedsTrailingNewline(term, p.peek()) {
			p.expectNewline()
		}
		return &ast.GrammarAssignTerm{Position: pos, Name: name, Term: term}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		term := p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
		if grammarTermNeedsTrailingNewline(term, p.peek()) {
			p.expectNewline()
		}
		return &ast.GrammarBindTerm{Position: pos, Name: name, Term: term}
	}
	term := p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
	if grammarTermNeedsTrailingNewline(term, p.peek()) {
		p.expectNewline()
	}
	return term
}

func grammarTermNeedsTrailingNewline(term ast.GrammarTerm, next lexer.TokenKind) bool {
	switch term.(type) {
	case *ast.GrammarPrecedenceTerm, *ast.GrammarPostfixTerm, *ast.GrammarSuffixTerm, *ast.GrammarSeqTerm:
		return false
	case *ast.GrammarChoiceTerm:
		return next == lexer.TOKEN_NEWLINE
	default:
		return true
	}
}

func (p *Parser) parseGrammarNestedTerm() ast.GrammarTerm {
	if p.peek() == lexer.TOKEN_PASS {
		pos := p.cur().Pos
		p.advance()
		return &ast.GrammarPassTerm{Position: pos}
	}
	if p.peekGrammarTokenKindBinding() {
		return p.parseGrammarTokenKindBinding()
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LARROW {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_LARROW)
		term := p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
		return &ast.GrammarAssignTerm{Position: pos, Name: name, Term: term}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		term := p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
		return &ast.GrammarBindTerm{Position: pos, Name: name, Term: term}
	}
	return p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
}

func (p *Parser) parseGrammarRecoverableTermValue() ast.GrammarTerm {
	return p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
}

func (p *Parser) peekGrammarBlockTerm(name string) bool {
	return p.peekIdentText(name) && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_LPAREN
}

func (p *Parser) peekGrammarPrecedenceTerm() bool {
	if !p.peekIdentText("precedence") || p.pos+1 >= len(p.tokens) {
		return false
	}
	next := p.tokens[p.pos+1]
	if next.Kind == lexer.TOKEN_LPAREN {
		return true
	}
	if next.Kind != lexer.TOKEN_IDENT {
		return false
	}
	if next.Text != ast.GrammarAssociativityLeft && next.Text != ast.GrammarAssociativityRight && next.Text != ast.GrammarAssociativityNonAssoc {
		return false
	}
	return p.pos+2 < len(p.tokens) && p.tokens[p.pos+2].Kind == lexer.TOKEN_LPAREN
}

func (p *Parser) parseGrammarTermValue() ast.GrammarTerm {
	term := p.parseGrammarChoiceTermValue()
	for {
		switch {
		case p.match(lexer.TOKEN_QUESTION):
			term = &ast.GrammarOptionalTerm{Position: term.Pos(), Term: term}
		case p.match(lexer.TOKEN_STAR):
			var until []ast.GrammarTerm
			if p.peekIdentText("until") {
				until = p.parseGrammarUntilClause()
			}
			term = &ast.GrammarRepeatTerm{Position: term.Pos(), Elem: term, Until: until}
		default:
			return term
		}
	}
}

func (p *Parser) parseGrammarChoiceTermValue() ast.GrammarTerm {
	term := p.parseGrammarConcatTermValue()
	if p.peek() == lexer.TOKEN_PIPE {
		options := []ast.GrammarTerm{term}
		for p.match(lexer.TOKEN_PIPE) {
			options = append(options, p.parseGrammarConcatTermValue())
		}
		term = &ast.GrammarChoiceTerm{Position: term.Pos(), Options: options}
	}
	return term
}

func (p *Parser) parseGrammarConcatTermValue() ast.GrammarTerm {
	term := p.parseGrammarAtomicTermValue()
	if !p.match(lexer.TOKEN_PLUS) {
		return term
	}
	terms := []ast.GrammarTerm{term, p.parseGrammarAtomicTermValue()}
	for p.match(lexer.TOKEN_PLUS) {
		terms = append(terms, p.parseGrammarAtomicTermValue())
	}
	return &ast.GrammarConcatTerm{Position: term.Pos(), Terms: terms}
}

func (p *Parser) parseGrammarAtomicTermValue() ast.GrammarTerm {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_STRING_LIT {
		lit := p.advance()
		return &ast.GrammarTokenTerm{Position: pos, Value: lit.Text}
	}
	if p.peekGrammarTokenKindTerm() && !p.peekGrammarTokenKindBinding() {
		return p.parseGrammarTokenKindTerm()
	}
	if p.peekGrammarPrecedenceTerm() {
		return p.parseGrammarPrecedenceTerm()
	}
	if p.peekGrammarBlockTerm("suffix") {
		return p.parseGrammarSuffixTerm()
	}
	if p.peekGrammarBlockTerm("postfix") {
		return p.parseGrammarPostfixTerm()
	}
	if p.peekGrammarBlockTerm("infix") {
		return p.parseGrammarInfixTableTerm()
	}
	if p.peekIdentText("choice") {
		return p.parseGrammarChoiceTerm()
	}
	if p.peekIdentText("when") {
		return p.parseGrammarWhenTerm()
	}
	if p.peekIdentText("required") {
		return p.parseGrammarRequiredTerm()
	}
	if p.peekIdentText("delimited") {
		return p.parseGrammarDelimitedTerm()
	}
	if p.peekIdentText("seq") {
		return p.parseGrammarSeqTerm()
	}
	if p.peekIdentText("prefix") {
		return p.parseGrammarPrefixTerm()
	}
	if p.peekIdentText("lookahead") {
		return p.parseGrammarLookaheadTerm()
	}
	if p.peekIdentText("expr") {
		return p.parseGrammarExprTerm()
	}
	if p.peekIdentText("maplist") {
		return p.parseGrammarMapListTerm(false)
	}
	if p.peekIdentText("flatmaplist") {
		return p.parseGrammarMapListTerm(true)
	}
	if p.peekIdentText("guard") {
		return p.parseGrammarGuardTerm()
	}
	if p.peekIdentText("attempt") {
		return p.parseGrammarAttemptTerm()
	}
	if p.peekIdentText("apply") {
		return p.parseGrammarApplyTerm()
	}
	if p.peekIdentText("cut") {
		return p.parseGrammarCutTerm()
	}
	if p.peekIdentText("optional") {
		return p.parseGrammarOptionalTerm()
	}
	if p.peekIdentText("list") {
		return p.parseGrammarListTerm()
	}
	if p.peekIdentText("flatrepeat") {
		return p.parseGrammarFlatRepeatTerm()
	}
	if p.peekIdentText("repeat") {
		return p.parseGrammarRepeatTerm()
	}
	if p.peekIdentText("separated") {
		return p.parseGrammarSeparatedTerm()
	}
	name := p.parseQualifiedDeclName()
	explicit := false
	args := make([]ast.Expr, 0, 2)
	if p.match(lexer.TOKEN_LPAREN) {
		explicit = true
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				args = append(args, p.parseExpr())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	return &ast.GrammarCallTerm{Position: pos, Name: name, Explicit: explicit, Args: args}
}

func (p *Parser) parseGrammarApplyTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("apply")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_LPAREN)
	args := make([]ast.GrammarApplyArg, 0, 4)
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			argPos := p.cur().Pos
			name := ""
			if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
				name = p.advance().Text
				p.expect(lexer.TOKEN_COLON)
			}
			args = append(args, ast.GrammarApplyArg{Position: argPos, Name: name, Term: p.parseGrammarUntilStopTerm()})
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarApplyTerm{Position: pos, Name: name, Args: args}
}

func (p *Parser) parseGrammarInfixTableTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("infix")
	p.expect(lexer.TOKEN_LPAREN)
	tableName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarInfixTableTerm{Position: pos, TableName: tableName}
}

func (p *Parser) peekGrammarTokenKindTerm() bool {
	return p.peek() == lexer.TOKEN_DOT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT
}

func (p *Parser) peekGrammarTokenKindBinding() bool {
	return p.peekGrammarTokenKindTerm() && p.pos+4 < len(p.tokens) && p.tokens[p.pos+2].Kind == lexer.TOKEN_LPAREN && p.tokens[p.pos+3].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+4].Kind == lexer.TOKEN_RPAREN
}

func (p *Parser) parseGrammarTokenKindTerm() *ast.GrammarTokenKindTerm {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_DOT)
	kind := p.expect(lexer.TOKEN_IDENT).Text
	return &ast.GrammarTokenKindTerm{Position: pos, Kind: kind}
}

func (p *Parser) parseGrammarTokenKindBinding() *ast.GrammarBindTerm {
	term := p.parseGrammarTokenKindTerm()
	p.expect(lexer.TOKEN_LPAREN)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarBindTerm{Position: term.Position, Name: name, Term: term}
}

func (p *Parser) parseGrammarChoiceTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("choice")
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		p.expect(lexer.TOKEN_INDENT)
		options := make([]ast.GrammarTerm, 0, 4)
		for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
			options = append(options, p.parseGrammarTerm())
		}
		p.expect(lexer.TOKEN_DEDENT)
		return &ast.GrammarChoiceTerm{Position: pos, Options: options}
	}
	p.expect(lexer.TOKEN_LPAREN)
	options := make([]ast.GrammarTerm, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			options = append(options, p.parseGrammarRecoverableTermValue())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarChoiceTerm{Position: pos, Options: options}
}

func (p *Parser) parseGrammarWhenTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("when")
	p.expect(lexer.TOKEN_LPAREN)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COMMA)
	thenTerm := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_COMMA)
	elseTerm := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarWhenTerm{Position: pos, Cond: cond, Then: thenTerm, Else: elseTerm}
}

func (p *Parser) parseGrammarRequiredTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("required")
	p.expect(lexer.TOKEN_LPAREN)
	term := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_COMMA)
	message := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarRequiredTerm{Position: pos, Term: term, Message: message}
}

func (p *Parser) parseGrammarDelimitedTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("delimited")
	p.expect(lexer.TOKEN_LPAREN)
	open := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_COMMA)
	body := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_COMMA)
	close := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_COMMA)
	message := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarDelimitedTerm{Position: pos, Open: open, Body: body, Close: close, Message: message}
}

func (p *Parser) parseGrammarSeqTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("seq")
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		return &ast.GrammarSeqTerm{Position: pos, Terms: p.parseGrammarTermBlock()}
	}
	p.expect(lexer.TOKEN_LPAREN)
	terms := make([]ast.GrammarTerm, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	if p.peek() != lexer.TOKEN_RPAREN {
		for p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_EOF {
			terms = append(terms, p.parseGrammarNestedTerm())
			if p.match(lexer.TOKEN_COMMA) {
				continue
			}
			if p.peek() == lexer.TOKEN_RPAREN || p.peek() == lexer.TOKEN_EOF {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarSeqTerm{Position: pos, Terms: terms}
}

func (p *Parser) parseGrammarPrefixTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("prefix")
	p.expect(lexer.TOKEN_LPAREN)
	ops := make([]ast.GrammarTerm, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			ops = append(ops, p.parseGrammarRecoverableTermValue())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	operand := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_ARROW)
	value := p.parseExpr()
	var opTerm ast.GrammarTerm
	if len(ops) == 1 {
		opTerm = ops[0]
	} else {
		opTerm = &ast.GrammarChoiceTerm{Position: pos, Options: ops}
	}
	return &ast.GrammarSeqTerm{Position: pos, Terms: []ast.GrammarTerm{
		&ast.GrammarBindTerm{Position: pos, Name: "op", Term: opTerm},
		&ast.GrammarBindTerm{Position: operand.Pos(), Name: "operand", Term: operand},
		&ast.GrammarExprTerm{Position: value.Pos(), Expr: value},
	}}
}

func (p *Parser) parseGrammarLookaheadTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("lookahead")
	p.expect(lexer.TOKEN_LPAREN)
	term := p.parseGrammarUntilStopTerm()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarLookaheadTerm{Position: pos, Term: term}
}

func (p *Parser) parseGrammarExprTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("expr")
	var typ ast.TypeExpr
	if p.match(lexer.TOKEN_LBRACKET) {
		typ = p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
	}
	p.expect(lexer.TOKEN_LPAREN)
	expr := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarExprTerm{Position: pos, Type: typ, Expr: expr}
}

func (p *Parser) parseGrammarMapListTerm(flatten bool) ast.GrammarTerm {
	pos := p.cur().Pos
	keyword := "maplist"
	if flatten {
		keyword = "flatmaplist"
	}
	p.expectIdentText(keyword)
	var typ ast.TypeExpr
	if p.match(lexer.TOKEN_LBRACKET) {
		typ = p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
	}
	p.expect(lexer.TOKEN_LPAREN)
	source := p.parseExpr()
	p.expect(lexer.TOKEN_COMMA)
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COMMA)
	value := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarMapListTerm{Position: pos, Type: typ, Source: source, Name: name, Value: value, Flatten: flatten}
}

func (p *Parser) parseGrammarGuardTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("guard")
	p.expect(lexer.TOKEN_LPAREN)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarGuardTerm{Position: pos, Cond: cond}
}

func (p *Parser) parseGrammarAttemptTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("attempt")
	p.expect(lexer.TOKEN_LPAREN)
	expr := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarAttemptTerm{Position: pos, Expr: expr}
}

func (p *Parser) parseGrammarCutTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("cut")
	return &ast.GrammarCutTerm{Position: pos}
}

func (p *Parser) parseGrammarOptionalTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("optional")
	if p.peek() != lexer.TOKEN_LPAREN {
		term := p.parseGrammarRecoverableTermValue()
		return &ast.GrammarOptionalTerm{Position: pos, Term: term}
	}
	p.expect(lexer.TOKEN_LPAREN)
	term := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarOptionalTerm{Position: pos, Term: term}
}

func (p *Parser) parseGrammarListTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("list")
	if p.peek() != lexer.TOKEN_LPAREN {
		elem := p.parseGrammarRecoverableTermValue()
		var separator ast.GrammarTerm
		var until []ast.GrammarTerm
		if p.peekIdentText("separated") {
			p.expectIdentText("separated")
			p.expectIdentText("by")
			separator = p.parseGrammarRecoverableTermValue()
		}
		if p.peekIdentText("until") {
			until = p.parseGrammarUntilClause()
		}
		return &ast.GrammarListTerm{Position: pos, Elem: elem, Separator: separator, Until: until}
	}
	p.expect(lexer.TOKEN_LPAREN)
	elem := p.parseGrammarRecoverableTermValue()
	var separator ast.GrammarTerm
	var until []ast.GrammarTerm
	if p.match(lexer.TOKEN_COMMA) {
		if p.peekIdentText("until") {
			until = p.parseGrammarUntilClause()
		} else {
			separator = p.parseGrammarRecoverableTermValue()
			if p.match(lexer.TOKEN_COMMA) {
				until = p.parseGrammarUntilClause()
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarListTerm{Position: pos, Elem: elem, Separator: separator, Until: until}
}

func (p *Parser) parseGrammarRepeatTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("repeat")
	if p.peek() != lexer.TOKEN_LPAREN {
		elem := p.parseGrammarRecoverableTermValue()
		var until []ast.GrammarTerm
		if p.peekIdentText("until") {
			until = p.parseGrammarUntilClause()
		}
		return &ast.GrammarRepeatTerm{Position: pos, Elem: elem, Until: until}
	}
	p.expect(lexer.TOKEN_LPAREN)
	elem := p.parseGrammarRecoverableTermValue()
	var until []ast.GrammarTerm
	if p.match(lexer.TOKEN_COMMA) {
		until = p.parseGrammarUntilClause()
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarRepeatTerm{Position: pos, Elem: elem, Until: until}
}

func (p *Parser) parseGrammarFlatRepeatTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("flatrepeat")
	if p.peek() != lexer.TOKEN_LPAREN {
		elem := p.parseGrammarRecoverableTermValue()
		var until []ast.GrammarTerm
		if p.peekIdentText("until") {
			until = p.parseGrammarUntilClause()
		}
		return &ast.GrammarFlatRepeatTerm{Position: pos, Elem: elem, Until: until}
	}
	p.expect(lexer.TOKEN_LPAREN)
	elem := p.parseGrammarRecoverableTermValue()
	var until []ast.GrammarTerm
	if p.match(lexer.TOKEN_COMMA) {
		until = p.parseGrammarUntilClause()
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarFlatRepeatTerm{Position: pos, Elem: elem, Until: until}
}

func (p *Parser) parseGrammarSeparatedTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("separated")
	if p.peek() != lexer.TOKEN_LPAREN {
		elem := p.parseGrammarRecoverableTermValue()
		p.expectIdentText("by")
		separator := p.parseGrammarRecoverableTermValue()
		var until []ast.GrammarTerm
		if p.peekIdentText("until") {
			until = p.parseGrammarUntilClause()
		}
		return &ast.GrammarSeparatedTerm{Position: pos, Elem: elem, Separator: separator, Until: until}
	}
	p.expect(lexer.TOKEN_LPAREN)
	elem := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_COMMA)
	separator := p.parseGrammarRecoverableTermValue()
	var until []ast.GrammarTerm
	if p.match(lexer.TOKEN_COMMA) {
		until = p.parseGrammarUntilClause()
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarSeparatedTerm{Position: pos, Elem: elem, Separator: separator, Until: until}
}

func (p *Parser) parseGrammarUntilClause() []ast.GrammarTerm {
	p.expectIdentText("until")
	return p.parseGrammarUntilClauseBody()
}

func (p *Parser) parseGrammarUntilClauseBody() []ast.GrammarTerm {
	terms := make([]ast.GrammarTerm, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	p.expect(lexer.TOKEN_LPAREN)
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			terms = append(terms, p.parseGrammarUntilStopTerm())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return terms
}

func (p *Parser) parseGrammarUntilStopTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) {
		switch p.tokens[p.pos+1].Kind {
		case lexer.TOKEN_COMMA, lexer.TOKEN_RPAREN, lexer.TOKEN_NEWLINE, lexer.TOKEN_DEDENT, lexer.TOKEN_EOF:
			return &ast.GrammarTokenSetRefTerm{Position: pos, Name: p.advance().Text}
		}
	}
	return p.parseGrammarRecoverableTermValue()
}

func (p *Parser) parseGrammarPostfixTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("postfix")
	p.expect(lexer.TOKEN_LPAREN)
	leftName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_ASSIGN)
	seed := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_RPAREN)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	arms := make([]ast.GrammarPostfixArm, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseGrammarPostfixArm())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return &ast.GrammarPostfixTerm{Position: pos, LeftName: leftName, Seed: seed, Arms: arms}
}

func (p *Parser) parseGrammarSuffixTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("suffix")
	p.expect(lexer.TOKEN_LPAREN)
	leftName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_ASSIGN)
	seed := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_RPAREN)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	arms := make([]ast.GrammarPostfixArm, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseGrammarPostfixArm())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return &ast.GrammarSuffixTerm{Position: pos, LeftName: leftName, Seed: seed, Arms: arms}
}

func (p *Parser) parseGrammarPostfixArm() ast.GrammarPostfixArm {
	pos := p.cur().Pos
	opName := ""
	var op ast.GrammarTerm
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		opName = p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		op = p.parseGrammarRecoverableTermValue()
	} else {
		op = p.parseGrammarRecoverableTermValue()
	}
	bindings := make([]*ast.GrammarBindTerm, 0, 2)
	for {
		if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
			bindPos := p.cur().Pos
			name := p.expect(lexer.TOKEN_IDENT).Text
			p.expect(lexer.TOKEN_ASSIGN)
			term := p.parseGrammarRecoverableTermValue()
			bindings = append(bindings, &ast.GrammarBindTerm{Position: bindPos, Name: name, Term: term})
			continue
		}
		if p.peekGrammarTokenKindBinding() {
			bindings = append(bindings, p.parseGrammarTokenKindBinding())
			continue
		}
		break
	}
	p.expect(lexer.TOKEN_ARROW)
	value := p.parseExpr()
	p.expectNewline()
	return ast.GrammarPostfixArm{Position: pos, OpName: opName, Op: op, Bindings: bindings, Value: value}
}

func (p *Parser) parseGrammarPrecedenceTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("precedence")
	assoc := ""
	if p.peekIdentText(ast.GrammarAssociativityLeft) || p.peekIdentText(ast.GrammarAssociativityRight) || p.peekIdentText(ast.GrammarAssociativityNonAssoc) {
		assoc = p.cur().Text
		p.advance()
	}
	p.expect(lexer.TOKEN_LPAREN)
	name := p.expect(lexer.TOKEN_IDENT).Text
	if p.match(lexer.TOKEN_ASSIGN) {
		seed := p.parseGrammarRecoverableTermValue()
		p.expect(lexer.TOKEN_RPAREN)
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		p.expect(lexer.TOKEN_INDENT)
		arms := make([]ast.GrammarPrecedenceArm, 0, p.estimateIndentedItemCount())
		for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
			p.skipNewlines()
			if p.peek() == lexer.TOKEN_DEDENT {
				break
			}
			arms = append(arms, p.parseGrammarPrecedenceArm())
		}
		p.expect(lexer.TOKEN_DEDENT)
		return &ast.GrammarPrecedenceTerm{Position: pos, Assoc: assoc, LeftName: name, Seed: seed, Arms: arms}
	}
	if assoc != "" {
		p.errorf("precedence associativity requires inline looping precedence")
	}
	p.expect(lexer.TOKEN_RPAREN)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	levels := make([]ast.GrammarPrecedenceLevel, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		levels = append(levels, p.parseGrammarPrecedenceLevel())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return &ast.GrammarPrecedenceTerm{Position: pos, Result: name, Levels: levels}
}

func (p *Parser) parseGrammarPrecedenceLevel() ast.GrammarPrecedenceLevel {
	pos := p.cur().Pos
	assoc := ""
	if p.peekIdentText(ast.GrammarAssociativityLeft) || p.peekIdentText(ast.GrammarAssociativityRight) || p.peekIdentText(ast.GrammarAssociativityNonAssoc) {
		assoc = p.cur().Text
		p.advance()
		if p.peek() == lexer.TOKEN_IDENT {
			pos = p.cur().Pos
		}
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	if p.match(lexer.TOKEN_ASSIGN) {
		if assoc != "" {
			p.errorf("precedence associativity requires a looping level")
			assoc = ""
		}
		seed := p.parseGrammarRecoverableTermValue()
		if grammarTermNeedsTrailingNewline(seed, p.peek()) {
			p.expectNewline()
		}
		return ast.GrammarPrecedenceLevel{Position: pos, Assoc: assoc, Name: name, Seed: seed}
	}
	p.expect(lexer.TOKEN_LPAREN)
	leftName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_ASSIGN)
	seed := p.parseGrammarRecoverableTermValue()
	p.expect(lexer.TOKEN_RPAREN)
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	arms := make([]ast.GrammarPrecedenceArm, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		arms = append(arms, p.parseGrammarPrecedenceArm())
	}
	p.expect(lexer.TOKEN_DEDENT)
	return ast.GrammarPrecedenceLevel{Position: pos, Assoc: assoc, Name: name, LeftName: leftName, Seed: seed, Arms: arms}
}

func (p *Parser) parseGrammarPrecedenceArm() ast.GrammarPrecedenceArm {
	pos := p.cur().Pos
	opName := ""
	var op ast.GrammarTerm
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		opName = p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		op = p.parseGrammarRecoverableTermValue()
	} else {
		op = p.parseGrammarRecoverableTermValue()
	}
	bindings := make([]*ast.GrammarBindTerm, 0, 2)
	for {
		if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
			bindPos := p.cur().Pos
			name := p.expect(lexer.TOKEN_IDENT).Text
			p.expect(lexer.TOKEN_ASSIGN)
			term := p.parseGrammarRecoverableTermValue()
			bindings = append(bindings, &ast.GrammarBindTerm{Position: bindPos, Name: name, Term: term})
			continue
		}
		if p.peekGrammarTokenKindBinding() {
			bindings = append(bindings, p.parseGrammarTokenKindBinding())
			continue
		}
		break
	}
	p.expect(lexer.TOKEN_ARROW)
	value := p.parseExpr()
	p.expectNewline()
	return ast.GrammarPrecedenceArm{Position: pos, OpName: opName, Op: op, Bindings: bindings, Value: value}
}
