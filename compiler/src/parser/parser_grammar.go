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
	var tokenEnumName string
	var tokenEnumStorage ast.TypeExpr
	var eofExpr ast.Expr
	var tokenKindField string
	var currentFunc string
	var advanceFunc string
	var expectFunc string
	var expectKindFunc string
	var recordErrorFunc string
	var tokenLookupFunc string
	tokenAliases := make([]ast.GrammarTokenAliasDecl, 0)
	channels := make([]ast.GrammarChannelDecl, 0)
	tokenSets := make([]ast.GrammarTokenSetDecl, 0)
	grammarAliases := make([]ast.GrammarAliasDecl, 0)
	grammarFns := make([]ast.GrammarFnDecl, 0)
	recoveryPolicies := make([]ast.GrammarRecoveryDecl, 0)
	infixTables := make([]ast.GrammarInfixTableDecl, 0)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if !p.peekGrammarConfigDecl() {
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
		case p.peekIdentText("token_enum"):
			tokenEnumName, tokenEnumStorage = p.parseGrammarTokenEnumHeaderDecl()
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
		case p.peekIdentText("token_lookup"):
			tokenLookupFunc = p.parseGrammarNameHeaderDecl("token_lookup")
		default:
			p.errorf("expected grammar environment declaration")
			p.advance()
		}
	}

	productions := make([]ast.GrammarProductionDecl, 0, p.estimateIndentedItemCount())
	parseSupportDecl := func() bool {
		switch {
		case p.peekGrammarTokenFamilyDecl():
			tokenSets = append(tokenSets, p.parseGrammarTokenFamilyDecl())
		case p.peekIdentText("token"):
			tokenAliases = append(tokenAliases, p.parseGrammarTokenAliasDecls()...)
		case p.peekIdentText("channel"):
			channels = append(channels, p.parseGrammarChannelDecl())
		case p.peekIdentText("tokenset"):
			tokenSets = append(tokenSets, p.parseGrammarTokenSetDecl())
		case p.peekGrammarAliasDecl():
			grammarAliases = append(grammarAliases, p.parseGrammarAliasDecl())
		case p.peekGrammarHelperDecl():
			grammarFns = append(grammarFns, p.parseGrammarHelperDecl())
		case p.peekIdentText("grammarfn"):
			grammarFns = append(grammarFns, p.parseGrammarFnDecl(false))
		case p.peekGrammarTypeDecl():
			grammarFns = append(grammarFns, p.parseGrammarFnDecl(true))
		case p.peekIdentText("recovery"):
			recoveryPolicies = append(recoveryPolicies, p.parseGrammarRecoveryDecl())
		case p.peekGrammarInfixTableDecl():
			infixTables = append(infixTables, p.parseGrammarInfixTableDecl())
		default:
			return false
		}
		return true
	}
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if parseSupportDecl() {
			continue
		}
		productions = append(productions, p.parseGrammarProductionDecls()...)
	}
	p.expect(lexer.TOKEN_DEDENT)
	p.validateGrammarAliasCycles(grammarAliases)
	p.validateGrammarFnApplications(grammarFns, grammarAliases, tokenSets, productions)
	p.validateGrammarProductionBodies(grammarFns, productions)

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
		TokenEnumName:    tokenEnumName,
		TokenEnumStorage: tokenEnumStorage,
		EOFExpr:          eofExpr,
		TokenKindField:   tokenKindField,
		CurrentFunc:      currentFunc,
		AdvanceFunc:      advanceFunc,
		ExpectFunc:       expectFunc,
		ExpectKindFunc:   expectKindFunc,
		RecordErrorFunc:  recordErrorFunc,
		TokenLookupFunc:  tokenLookupFunc,
		TokenAliases:     tokenAliases,
		Channels:         channels,
		TokenSets:        tokenSets,
		GrammarAliases:   grammarAliases,
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

func (p *Parser) peekGrammarTypeDecl() bool {
	return p.peekIdentText("grammar") && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "type"
}

func (p *Parser) peekGrammarAliasDecl() bool {
	return p.peekIdentText("grammar") && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "alias"
}

func (p *Parser) peekGrammarTokenFamilyDecl() bool {
	return p.peekIdentText("token") && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT && p.tokens[p.pos+1].Text == "family"
}

func (p *Parser) peekGrammarHelperDecl() bool {
	if !p.peekIdentText("grammar") || p.pos+1 >= len(p.tokens) {
		return false
	}
	next := p.tokens[p.pos+1]
	return next.Kind == lexer.TOKEN_IDENT && next.Text != "type" && next.Text != "alias"
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
	var tokenGrammarName string
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
		case p.peekIdentText("tokens"):
			p.expectIdentText("tokens")
			tokenGrammarName = p.parseQualifiedDeclName()
			p.expectNewline()
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
		Position:         pos,
		Name:             name,
		OverType:         overType,
		UsingType:        usingType,
		ErrorType:        errorType,
		CursorExpr:       cursorExpr,
		AllocExpr:        allocExpr,
		TokenKindType:    tokenKindType,
		TokenGrammarName: tokenGrammarName,
		EOFExpr:          eofExpr,
		TokenKindField:   tokenKindField,
		CurrentFunc:      currentFunc,
		AdvanceFunc:      advanceFunc,
		ExpectFunc:       expectFunc,
		ExpectKindFunc:   expectKindFunc,
		RecordErrorFunc:  recordErrorFunc,
	}
}

func (p *Parser) peekGrammarHeaderDecl() bool {
	return p.peekGrammarConfigDecl() || p.peekGrammarSupportDecl()
}

func (p *Parser) peekGrammarConfigDecl() bool {
	if p.peek() == lexer.TOKEN_ERROR {
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
	case "cursor", "alloc", "token_kind", "token_enum", "eof", "token_field", "current", "advance", "expect", "expect_kind", "record_error", "token_lookup":
		return next != lexer.TOKEN_LPAREN
	default:
		return false
	}
}

func (p *Parser) peekGrammarSupportDecl() bool {
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
	case "token", "channel":
		return next != lexer.TOKEN_LPAREN
	case "tokenset":
		return next == lexer.TOKEN_IDENT
	case "grammarfn":
		return next == lexer.TOKEN_IDENT
	case "grammar":
		return p.peekGrammarTypeDecl() || p.peekGrammarAliasDecl() || p.peekGrammarHelperDecl()
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

func (p *Parser) parseGrammarTokenEnumHeaderDecl() (string, ast.TypeExpr) {
	p.expectIdentText("token_enum")
	name := p.parseQualifiedDeclName()
	var storage ast.TypeExpr
	if p.matchIdentText("of") {
		storage = p.parseGrammarHeaderTypeExpr()
	}
	p.expectNewline()
	return name, storage
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
	return p.finishGrammarTokenSetDecl(pos, p.expect(lexer.TOKEN_IDENT).Text, false)
}

func (p *Parser) parseGrammarTokenFamilyDecl() ast.GrammarTokenSetDecl {
	pos := p.cur().Pos
	p.expectIdentText("token")
	p.expectIdentText("family")
	return p.finishGrammarTokenSetDecl(pos, p.expect(lexer.TOKEN_IDENT).Text, true)
}

func (p *Parser) finishGrammarTokenSetDecl(pos lexer.Pos, name string, tokenFamily bool) ast.GrammarTokenSetDecl {
	terms := make([]ast.GrammarTerm, 0, 4)
	if p.match(lexer.TOKEN_ASSIGN) {
		for {
			terms = append(terms, p.parseGrammarTokenSetItem())
			if !p.match(lexer.TOKEN_COMMA) && !p.match(lexer.TOKEN_PIPE) {
				break
			}
		}
		p.expectNewline()
		return ast.GrammarTokenSetDecl{Position: pos, Name: name, TokenFamily: tokenFamily, Terms: terms}
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
	return ast.GrammarTokenSetDecl{Position: pos, Name: name, TokenFamily: tokenFamily, Terms: terms}
}

func (p *Parser) parseGrammarTokenSetItem() ast.GrammarTerm {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind != lexer.TOKEN_LPAREN {
		return &ast.GrammarTokenSetRefTerm{Position: pos, Name: p.advance().Text}
	}
	if p.peekIdentText("first") {
		return p.parseGrammarFirstTerm()
	}
	return p.parseGrammarRecoverableTermValue()
}

func (p *Parser) parseGrammarFirstTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("first")
	p.expect(lexer.TOKEN_LPAREN)
	name := p.parseQualifiedDeclName()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarFirstTerm{Position: pos, Name: name}
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

func (p *Parser) parseGrammarAliasDecl() ast.GrammarAliasDecl {
	pos := p.cur().Pos
	p.expectIdentText("grammar")
	p.expectIdentText("alias")
	name := p.expect(lexer.TOKEN_IDENT).Text
	var params []ast.GrammarFnParam
	if p.match(lexer.TOKEN_LPAREN) {
		params = p.parseGrammarFnParamsUntilRParen()
	}
	var term ast.GrammarTerm
	if p.match(lexer.TOKEN_ASSIGN) {
		term = p.parseGrammarRecoverableTermValue()
	} else {
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		terms := p.parseGrammarTermBlock()
		if len(terms) == 1 {
			term = terms[0]
		} else {
			term = &ast.GrammarSeqTerm{Position: pos, Terms: terms}
		}
		return ast.GrammarAliasDecl{Position: pos, Name: name, Params: params, Term: term}
	}
	p.expectNewline()
	return ast.GrammarAliasDecl{Position: pos, Name: name, Params: params, Term: term}
}

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
		case *ast.GrammarChoiceTerm:
			for _, option := range n.Options {
				walk(option)
			}
		case *ast.GrammarOptionalTerm:
			walk(n.Term)
		case *ast.GrammarWhenTerm:
			walk(n.Then)
			walk(n.Else)
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
	typeParams, _, _, _, _, genericParams := p.parseFuncGenericParams()
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
	typeParams, _, _, _, _, genericParams := p.parseFuncGenericParams()
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
	case *ast.GrammarChoiceTerm:
		p.validateGrammarFnApplicationsInTerms(n.Options, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarOptionalTerm:
		p.validateGrammarFnApplicationInTerm(n.Term, grammarFns, aliases, tokenSetNames)
	case *ast.GrammarWhenTerm:
		p.validateGrammarFnApplicationInTerm(n.Then, grammarFns, aliases, tokenSetNames)
		p.validateGrammarFnApplicationInTerm(n.Else, grammarFns, aliases, tokenSetNames)
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

func (p *Parser) validateGrammarProductionTerm(productionName string, term ast.GrammarTerm) {
	switch n := term.(type) {
	case *ast.GrammarBindTerm:
		p.validateGrammarProductionTerm(productionName, n.Term)
	case *ast.GrammarAssignTerm:
		p.validateGrammarProductionTerm(productionName, n.Term)
	case *ast.GrammarReturnTerm:
		// valid
	case *ast.GrammarPassTerm:
		// valid
	case *ast.GrammarSeqTerm:
		for _, seqTerm := range n.Terms {
			p.validateGrammarProductionTerm(productionName, seqTerm)
		}
	case *ast.GrammarChoiceTerm:
		for _, opt := range n.Options {
			p.validateGrammarProductionTerm(productionName, opt)
		}
	case *ast.GrammarOptionalTerm, *ast.GrammarRequiredTerm, *ast.GrammarDelimitedTerm,
		*ast.GrammarLookaheadTerm, *ast.GrammarGuardTerm, *ast.GrammarAttemptTerm,
		*ast.GrammarWhenTerm, *ast.GrammarRecoverTerm:
		// valid
	case *ast.GrammarListTerm, *ast.GrammarRepeatTerm, *ast.GrammarFlatRepeatTerm, *ast.GrammarSeparatedTerm:
		// valid
	case *ast.GrammarSuffixTerm, *ast.GrammarPostfixTerm, *ast.GrammarPrecedenceTerm:
		// valid
	case *ast.GrammarExprTerm, *ast.GrammarMapListTerm, *ast.GrammarSingletonTerm, *ast.GrammarEmptyTerm,
		*ast.GrammarConcatTerm, *ast.GrammarCutTerm:
		// valid
	case *ast.GrammarCallTerm, *ast.GrammarApplyTerm, *ast.GrammarTokenTerm, *ast.GrammarTokenKindTerm,
		*ast.GrammarTokenSetRefTerm, *ast.GrammarFirstTerm, *ast.GrammarInfixTableTerm:
		// valid
	default:
		pos := term.Pos()
		p.errorAt(pos, "grammar production %q contains an unsupported construct at %s; expected grammar terms such as token matches, bindings, choices, sequences, or helper calls", productionName, pos)
	}
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
	channels, terms := p.parseGrammarProductionBody()
	return []ast.GrammarProductionDecl{{Position: pos, Public: public, Name: name, HasParamList: hasParamList, Params: params, ReturnType: retType, RecoverPolicy: recoverPolicy, RecoverMsg: recoverMsg, RecoverUntil: recoverUntil, RecoverValue: recoverValue, Channels: channels, Terms: terms}}
}

func (p *Parser) parseGrammarProductionBody() ([]ast.GrammarChannelDecl, []ast.GrammarTerm) {
	p.expect(lexer.TOKEN_INDENT)
	channels := make([]ast.GrammarChannelDecl, 0)
	terms := make([]ast.GrammarTerm, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peekIdentText("channel") {
			channels = append(channels, p.parseGrammarChannelDecl())
			continue
		}
		term := p.parseGrammarTerm()
		if term != nil {
			terms = append(terms, term)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return channels, terms
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
	if p.peek() == lexer.TOKEN_MUTABLE || p.peek() == lexer.TOKEN_IF || p.peek() == lexer.TOKEN_WHILE || p.peek() == lexer.TOKEN_MATCH {
		pos := p.cur().Pos
		keyword := p.cur().Text
		p.errorAt(pos, "grammar production body cannot contain general statements (found %q); grammar terms should use token matches, bindings, choices, or helper calls, not general control flow or local variable declarations", keyword)
		p.advance()
		return nil
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
	term := p.parseGrammarPipelineTermValue()
	if p.peek() == lexer.TOKEN_PIPE {
		options := []ast.GrammarTerm{term}
		for p.match(lexer.TOKEN_PIPE) {
			options = append(options, p.parseGrammarPipelineTermValue())
		}
		term = &ast.GrammarChoiceTerm{Position: term.Pos(), Options: options}
	}
	return term
}

func (p *Parser) parseGrammarPipelineTermValue() ast.GrammarTerm {
	term := p.parseGrammarConcatTermValue()
	for p.peek() == lexer.TOKEN_PIPEGT {
		term = p.parseGrammarPipelineStep(term)
	}
	return term
}

func (p *Parser) parseGrammarPipelineStep(input ast.GrammarTerm) ast.GrammarTerm {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_PIPEGT)
	name := p.parseQualifiedDeclName()
	if !p.match(lexer.TOKEN_LPAREN) {
		p.errorAt(pos, "expected '(' after grammar pipeline target")
		return input
	}
	args := []ast.GrammarApplyArg{{Position: input.Pos(), Term: input}}
	args = append(args, p.parseGrammarApplyArgsUntilRParen()...)
	return &ast.GrammarApplyTerm{Position: pos, Name: name, Direct: true, Piped: true, Args: args}
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
	if p.peekIdentText("first") {
		return p.parseGrammarFirstTerm()
	}
	if p.peekIdentText("singleton") {
		return p.parseGrammarSingletonTerm()
	}
	if p.peekIdentText("empty") {
		return p.parseGrammarEmptyTerm()
	}
	if p.peekIdentText("none") {
		return p.parseGrammarNoneTerm()
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
		if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON {
			applyArgs := p.parseGrammarApplyArgsUntilRParen()
			return &ast.GrammarApplyTerm{Position: pos, Name: name, Direct: true, Args: applyArgs}
		}
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
	args := p.parseGrammarApplyArgsUntilRParen()
	return &ast.GrammarApplyTerm{Position: pos, Name: name, Args: args}
}

func (p *Parser) parseGrammarApplyArgsUntilRParen() []ast.GrammarApplyArg {
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
	return args
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
			p.skipNewlines()
			if p.peek() == lexer.TOKEN_DEDENT {
				break
			}
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

func (p *Parser) parseGrammarSingletonTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("singleton")
	var typ ast.TypeExpr
	if p.match(lexer.TOKEN_LBRACKET) {
		typ = p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
	}
	p.expect(lexer.TOKEN_LPAREN)
	value := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarSingletonTerm{Position: pos, Type: typ, Value: value}
}

func (p *Parser) parseGrammarEmptyTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("empty")
	var typ ast.TypeExpr
	if p.match(lexer.TOKEN_LBRACKET) {
		typ = p.parseTypeExpr()
		p.expect(lexer.TOKEN_RBRACKET)
	}
	return &ast.GrammarEmptyTerm{Position: pos, Type: typ}
}

func (p *Parser) parseGrammarNoneTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("none")
	p.expect(lexer.TOKEN_LBRACKET)
	typ := p.parseTypeExpr()
	p.expect(lexer.TOKEN_RBRACKET)
	return &ast.GrammarExprTerm{Position: pos, Type: &ast.OptionalTypeExpr{Position: pos, Value: typ}, Expr: &ast.NullLit{Position: pos}}
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
		if p.tokens[p.pos].Text == "first" && p.tokens[p.pos+1].Kind == lexer.TOKEN_LPAREN {
			return p.parseGrammarFirstTerm()
		}
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
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		p.expect(lexer.TOKEN_INDENT)
		bindings := make([]*ast.GrammarBindTerm, 0, 2)
		for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
			p.skipNewlines()
			if p.peek() == lexer.TOKEN_DEDENT {
				break
			}
			if p.peek() == lexer.TOKEN_ARROW {
				break
			}
			if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
				bindPos := p.cur().Pos
				name := p.expect(lexer.TOKEN_IDENT).Text
				p.expect(lexer.TOKEN_ASSIGN)
				term := p.parseGrammarRecoverableTermValue()
				bindings = append(bindings, &ast.GrammarBindTerm{Position: bindPos, Name: name, Term: term})
				p.expectNewline()
				continue
			}
			if p.peekGrammarTokenKindBinding() {
				bindings = append(bindings, p.parseGrammarTokenKindBinding())
				p.expectNewline()
				continue
			}
			p.errorAt(p.cur().Pos, "expected postfix arm binding or -> expression")
			break
		}
		p.expect(lexer.TOKEN_ARROW)
		value := p.parseExpr()
		p.expectNewline()
		p.expect(lexer.TOKEN_DEDENT)
		return ast.GrammarPostfixArm{Position: pos, OpName: opName, Op: op, Block: true, Bindings: bindings, Value: value}
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
	if p.match(lexer.TOKEN_COLON) {
		p.expectNewline()
		p.expect(lexer.TOKEN_INDENT)
		bindings := make([]*ast.GrammarBindTerm, 0, 2)
		for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
			p.skipNewlines()
			if p.peek() == lexer.TOKEN_DEDENT {
				break
			}
			if p.peek() == lexer.TOKEN_ARROW {
				break
			}
			if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
				bindPos := p.cur().Pos
				name := p.expect(lexer.TOKEN_IDENT).Text
				p.expect(lexer.TOKEN_ASSIGN)
				term := p.parseGrammarRecoverableTermValue()
				bindings = append(bindings, &ast.GrammarBindTerm{Position: bindPos, Name: name, Term: term})
				p.expectNewline()
				continue
			}
			if p.peekGrammarTokenKindBinding() {
				bindings = append(bindings, p.parseGrammarTokenKindBinding())
				p.expectNewline()
				continue
			}
			p.errorAt(p.cur().Pos, "expected precedence arm binding or -> expression")
			break
		}
		p.expect(lexer.TOKEN_ARROW)
		value := p.parseExpr()
		p.expectNewline()
		p.expect(lexer.TOKEN_DEDENT)
		return ast.GrammarPrecedenceArm{Position: pos, OpName: opName, Op: op, Block: true, Bindings: bindings, Value: value}
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
