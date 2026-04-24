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
	var overType ast.TypeExpr
	var usingType ast.TypeExpr
	uses := make([]ast.TypeExpr, 0)
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
	tokenAliases := make([]ast.GrammarTokenAliasDecl, 0)
	channels := make([]ast.GrammarChannelDecl, 0)
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
		case p.peekIdentText("token"):
			tokenAliases = append(tokenAliases, p.parseGrammarTokenAliasDecls()...)
		case p.peekIdentText("channel"):
			channels = append(channels, p.parseGrammarChannelDecl())
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
		OverType:         overType,
		UsingType:        usingType,
		Uses:             uses,
		ErrorType:        errorType,
		CursorExpr:       cursorExpr,
		AllocExpr:        allocExpr,
		TokenAliases:     tokenAliases,
		Channels:         channels,
		Productions:      productions,
	}
}

func (p *Parser) peekGrammarHeaderDecl() bool {
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
	case "cursor", "alloc", "token", "channel":
		return next != lexer.TOKEN_LPAREN
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
	var recoverMsg ast.Expr
	var recoverUntil []ast.GrammarTerm
	var recoverValue ast.Expr
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}
	if p.peekIdentText("recover") {
		recoverMsg, recoverUntil, recoverValue = p.parseGrammarRecoverClause()
	}

	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	terms := p.parseGrammarTermBlock()
	return []ast.GrammarProductionDecl{{Position: pos, Public: public, Name: name, HasParamList: hasParamList, Params: params, ReturnType: retType, RecoverMsg: recoverMsg, RecoverUntil: recoverUntil, RecoverValue: recoverValue, Terms: terms}}
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

func (p *Parser) parseGrammarRecoverClause() (ast.Expr, []ast.GrammarTerm, ast.Expr) {
	p.expectIdentText("recover")
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
	message, until, fallback := p.parseGrammarRecoverClause()
	return &ast.GrammarRecoverTerm{
		Position:     term.Pos(),
		Term:         term,
		RecoverMsg:   message,
		RecoverUntil: until,
		RecoverValue: fallback,
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
	if p.peekGrammarBlockTerm("precedence") {
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
		if _, ok := term.(*ast.GrammarPrecedenceTerm); !ok {
			if _, ok := term.(*ast.GrammarPostfixTerm); !ok {
				if _, ok := term.(*ast.GrammarSuffixTerm); !ok {
					if _, ok := term.(*ast.GrammarSeqTerm); !ok {
						p.expectNewline()
					}
				}
			}
		}
		return &ast.GrammarAssignTerm{Position: pos, Name: name, Term: term}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		term := p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
		if _, ok := term.(*ast.GrammarPrecedenceTerm); !ok {
			if _, ok := term.(*ast.GrammarPostfixTerm); !ok {
				if _, ok := term.(*ast.GrammarSuffixTerm); !ok {
					if _, ok := term.(*ast.GrammarSeqTerm); !ok {
						p.expectNewline()
					}
				}
			}
		}
		return &ast.GrammarBindTerm{Position: pos, Name: name, Term: term}
	}
	term := p.wrapGrammarRecoverTerm(p.parseGrammarTermValue())
	if _, ok := term.(*ast.GrammarSeqTerm); !ok {
		p.expectNewline()
	}
	return term
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
	if p.peekGrammarBlockTerm("precedence") {
		return p.parseGrammarPrecedenceTerm()
	}
	if p.peekGrammarBlockTerm("suffix") {
		return p.parseGrammarSuffixTerm()
	}
	if p.peekGrammarBlockTerm("postfix") {
		return p.parseGrammarPostfixTerm()
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
	term := p.parseGrammarRecoverableTermValue()
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
	terms := make([]ast.GrammarTerm, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	p.expectIdentText("until")
	p.expect(lexer.TOKEN_LPAREN)
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			terms = append(terms, p.parseGrammarRecoverableTermValue())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return terms
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
		return &ast.GrammarPrecedenceTerm{Position: pos, LeftName: name, Seed: seed, Arms: arms}
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
	name := p.expect(lexer.TOKEN_IDENT).Text
	if p.match(lexer.TOKEN_ASSIGN) {
		seed := p.parseGrammarRecoverableTermValue()
		if _, ok := seed.(*ast.GrammarPrecedenceTerm); !ok {
			p.expectNewline()
		}
		return ast.GrammarPrecedenceLevel{Position: pos, Name: name, Seed: seed}
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
	return ast.GrammarPrecedenceLevel{Position: pos, Name: name, LeftName: leftName, Seed: seed, Arms: arms}
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
