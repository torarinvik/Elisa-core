package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseGrammarDecl() *ast.GrammarDecl {
	pos := p.cur().Pos
	extend := p.peekIdentText("extend")
	if extend {
		p.expectIdentText("extend")
	}
	p.expectIdentText("grammar")
	name := p.expect(lexer.TOKEN_IDENT).Text
	typeParams, regionParams, permissionParams, genericParams := p.parseFuncGenericParams()
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
	var tokenLookupCompareFunc string
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
		case p.peekIdentText("token_lookup_compare"):
			tokenLookupCompareFunc = p.parseGrammarNameHeaderDecl("token_lookup_compare")
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
		Position:               pos,
		Extend:                 extend,
		Name:                   name,
		TypeParams:             typeParams,
		RegionParams:           regionParams,
		PermissionParams:       permissionParams,
		GenericParams:          genericParams,
		EnvType:                envType,
		OverType:               overType,
		UsingType:              usingType,
		Uses:                   uses,
		ErrorType:              errorType,
		CursorExpr:             cursorExpr,
		AllocExpr:              allocExpr,
		TokenKindType:          tokenKindType,
		TokenEnumName:          tokenEnumName,
		TokenEnumStorage:       tokenEnumStorage,
		EOFExpr:                eofExpr,
		TokenKindField:         tokenKindField,
		CurrentFunc:            currentFunc,
		AdvanceFunc:            advanceFunc,
		ExpectFunc:             expectFunc,
		ExpectKindFunc:         expectKindFunc,
		RecordErrorFunc:        recordErrorFunc,
		TokenLookupFunc:        tokenLookupFunc,
		TokenLookupCompareFunc: tokenLookupCompareFunc,
		TokenAliases:           tokenAliases,
		Channels:               channels,
		TokenSets:              tokenSets,
		GrammarAliases:         grammarAliases,
		GrammarFns:             grammarFns,
		RecoveryPolicies:       recoveryPolicies,
		InfixTables:            infixTables,
		Productions:            productions,
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
	// Conventional-name defaulting: a grammarenv only has to spell out cursor
	// (and alloc when used); everything else follows the standard host-state
	// conventions and can be overridden explicitly.
	if tokenKindType == nil {
		if overName, ok := grammarHeaderTypeName(overType); ok {
			tokenKindType = &ast.NamedType{Position: pos, Name: overName + "Kind"}
		}
	}
	if eofExpr == nil {
		if kindName, ok := grammarHeaderTypeName(tokenKindType); ok {
			eofExpr = &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: kindName}, Field: "EOF"}
		}
	}
	if tokenKindField == "" {
		tokenKindField = "kind"
	}
	if currentFunc == "" {
		currentFunc = "current_token"
	}
	if advanceFunc == "" {
		advanceFunc = "advance_token"
	}
	if expectFunc == "" {
		expectFunc = "expect"
	}
	if expectKindFunc == "" {
		expectKindFunc = "expect_kind"
	}
	if recordErrorFunc == "" {
		recordErrorFunc = "record_parse_error"
	}
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
	case "cursor", "alloc", "token_kind", "token_enum", "eof", "token_field", "current", "advance", "expect", "expect_kind", "record_error", "token_lookup", "token_lookup_compare":
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
	base := p.parseBaseType(ast.RefStorageAny, false, "", "")
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
	excluded := make([]ast.GrammarTerm, 0)
	if p.match(lexer.TOKEN_ASSIGN) {
		for {
			terms = append(terms, p.parseGrammarTokenSetItem())
			if p.match(lexer.TOKEN_MINUS) {
				excluded = append(excluded, p.parseGrammarTokenSetItem())
				for p.match(lexer.TOKEN_MINUS) {
					excluded = append(excluded, p.parseGrammarTokenSetItem())
				}
			}
			if !p.match(lexer.TOKEN_COMMA) && !p.match(lexer.TOKEN_PIPE) && !p.match(lexer.TOKEN_PLUS) {
				break
			}
		}
		p.expectNewline()
		return ast.GrammarTokenSetDecl{Position: pos, Name: name, TokenFamily: tokenFamily, Terms: terms, Excluded: excluded}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.match(lexer.TOKEN_MINUS) {
			excluded = append(excluded, p.parseGrammarTokenSetItem())
		} else {
			terms = append(terms, p.parseGrammarTokenSetItem())
		}
		p.expectNewline()
	}
	p.expect(lexer.TOKEN_DEDENT)
	return ast.GrammarTokenSetDecl{Position: pos, Name: name, TokenFamily: tokenFamily, Terms: terms, Excluded: excluded}
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
	normalizeItems := func(items []ast.GrammarTerm) []ast.GrammarTerm {
		if len(items) == 0 {
			return items
		}
		out := make([]ast.GrammarTerm, 0, len(items))
		for _, term := range items {
			ref, ok := term.(*ast.GrammarTokenSetRefTerm)
			if !ok || setNames[ref.Name] {
				out = append(out, term)
				continue
			}
			out = append(out, &ast.GrammarTokenKindTerm{Position: ref.Position, Kind: ref.Name})
		}
		return out
	}
	for _, tokenSet := range tokenSets {
		tokenSet.Terms = normalizeItems(tokenSet.Terms)
		tokenSet.Excluded = normalizeItems(tokenSet.Excluded)
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

// grammarHeaderTypeName extracts the plain name of a grammar header type
// (`over SMLToken`, `token_kind SMLTokenKind`); false for anything structured.
func grammarHeaderTypeName(t ast.TypeExpr) (string, bool) {
	named, ok := t.(*ast.NamedType)
	if !ok || named == nil || named.Name == "" || named.Name == "<invalid>" {
		return "", false
	}
	return named.Name, true
}
