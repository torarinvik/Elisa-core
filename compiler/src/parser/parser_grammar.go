package parser

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (p *Parser) parseGrammarDecl() *ast.GrammarDecl {
	pos := p.cur().Pos
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
		productions = append(productions, p.parseGrammarProductionDecl())
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.GrammarDecl{
		Position:         pos,
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
	case "cursor", "alloc", "channel":
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

func (p *Parser) parseGrammarProductionDecl() ast.GrammarProductionDecl {
	pos := p.cur().Pos
	public := p.peekIdentText("pub") && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT
	if public {
		p.expectIdentText("pub")
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	hasParamList := p.match(lexer.TOKEN_LPAREN)
	params := make([]ast.ParamDecl, 0)
	if hasParamList {
		params = p.parseParamList(false)
		p.expect(lexer.TOKEN_RPAREN)
	}

	var retType ast.TypeExpr
	var recoverMsg ast.Expr
	var recoverUntil []ast.GrammarTerm
	if p.match(lexer.TOKEN_ARROW) {
		retType = p.parseTypeExpr()
	}
	if p.peekIdentText("recover") {
		recoverMsg, recoverUntil = p.parseGrammarRecoverClause()
	}

	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	terms := p.parseGrammarTermBlock()
	return ast.GrammarProductionDecl{Position: pos, Public: public, Name: name, HasParamList: hasParamList, Params: params, ReturnType: retType, RecoverMsg: recoverMsg, RecoverUntil: recoverUntil, Terms: terms}
}

func (p *Parser) parseGrammarRecoverClause() (ast.Expr, []ast.GrammarTerm) {
	p.expectIdentText("recover")
	p.expect(lexer.TOKEN_LPAREN)
	message := p.parseExpr()
	p.expect(lexer.TOKEN_COMMA)
	until := p.parseGrammarUntilClause()
	p.expect(lexer.TOKEN_RPAREN)
	return message, until
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
	if p.peekIdentText("precedence") {
		return p.parseGrammarPrecedenceTerm()
	}
	if p.peekIdentText("postfix") {
		return p.parseGrammarPostfixTerm()
	}
	if p.peekGrammarTokenKindBinding() {
		binding := p.parseGrammarTokenKindBinding()
		p.expectNewline()
		return binding
	}
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		pos := p.cur().Pos
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		term := p.parseGrammarTermValue()
		if _, ok := term.(*ast.GrammarPrecedenceTerm); !ok {
			if _, ok := term.(*ast.GrammarPostfixTerm); !ok {
				p.expectNewline()
			}
		}
		return &ast.GrammarBindTerm{Position: pos, Name: name, Term: term}
	}
	term := p.parseGrammarTermValue()
	p.expectNewline()
	return term
}

func (p *Parser) parseGrammarTermValue() ast.GrammarTerm {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_STRING_LIT {
		lit := p.advance()
		return &ast.GrammarTokenTerm{Position: pos, Value: lit.Text}
	}
	if p.peekGrammarTokenKindTerm() && !p.peekGrammarTokenKindBinding() {
		return p.parseGrammarTokenKindTerm()
	}
	if p.peekIdentText("precedence") {
		return p.parseGrammarPrecedenceTerm()
	}
	if p.peekIdentText("postfix") {
		return p.parseGrammarPostfixTerm()
	}
	if p.peekIdentText("choice") {
		return p.parseGrammarChoiceTerm()
	}
	if p.peekIdentText("expr") {
		return p.parseGrammarExprTerm()
	}
	if p.peekIdentText("attempt") {
		return p.parseGrammarAttemptTerm()
	}
	if p.peekIdentText("optional") {
		return p.parseGrammarOptionalTerm()
	}
	if p.peekIdentText("list") {
		return p.parseGrammarListTerm()
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
			options = append(options, p.parseGrammarTermValue())
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarChoiceTerm{Position: pos, Options: options}
}

func (p *Parser) parseGrammarExprTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("expr")
	p.expect(lexer.TOKEN_LPAREN)
	expr := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarExprTerm{Position: pos, Expr: expr}
}

func (p *Parser) parseGrammarAttemptTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("attempt")
	p.expect(lexer.TOKEN_LPAREN)
	expr := p.parseExpr()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarAttemptTerm{Position: pos, Expr: expr}
}

func (p *Parser) parseGrammarOptionalTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("optional")
	p.expect(lexer.TOKEN_LPAREN)
	term := p.parseGrammarTermValue()
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarOptionalTerm{Position: pos, Term: term}
}

func (p *Parser) parseGrammarListTerm() ast.GrammarTerm {
	pos := p.cur().Pos
	p.expectIdentText("list")
	p.expect(lexer.TOKEN_LPAREN)
	elem := p.parseGrammarTermValue()
	var separator ast.GrammarTerm
	var until []ast.GrammarTerm
	if p.match(lexer.TOKEN_COMMA) {
		if p.peekIdentText("until") {
			until = p.parseGrammarUntilClause()
		} else {
			separator = p.parseGrammarTermValue()
			if p.match(lexer.TOKEN_COMMA) {
				until = p.parseGrammarUntilClause()
			}
		}
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.GrammarListTerm{Position: pos, Elem: elem, Separator: separator, Until: until}
}

func (p *Parser) parseGrammarUntilClause() []ast.GrammarTerm {
	terms := make([]ast.GrammarTerm, 0, p.estimateCommaSeparatedCount(lexer.TOKEN_RPAREN))
	p.expectIdentText("until")
	p.expect(lexer.TOKEN_LPAREN)
	if p.peek() != lexer.TOKEN_RPAREN {
		for {
			terms = append(terms, p.parseGrammarTermValue())
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
	seed := p.parseGrammarTermValue()
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

func (p *Parser) parseGrammarPostfixArm() ast.GrammarPostfixArm {
	pos := p.cur().Pos
	opName := ""
	var op ast.GrammarTerm
	if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
		opName = p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_ASSIGN)
		op = p.parseGrammarTermValue()
	} else {
		op = p.parseGrammarTermValue()
	}
	bindings := make([]*ast.GrammarBindTerm, 0, 2)
	for {
		if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
			bindPos := p.cur().Pos
			name := p.expect(lexer.TOKEN_IDENT).Text
			p.expect(lexer.TOKEN_ASSIGN)
			term := p.parseGrammarTermValue()
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
		seed := p.parseGrammarTermValue()
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
		seed := p.parseGrammarTermValue()
		if _, ok := seed.(*ast.GrammarPrecedenceTerm); !ok {
			p.expectNewline()
		}
		return ast.GrammarPrecedenceLevel{Position: pos, Name: name, Seed: seed}
	}
	p.expect(lexer.TOKEN_LPAREN)
	leftName := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_ASSIGN)
	seed := p.parseGrammarTermValue()
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
		op = p.parseGrammarTermValue()
	} else {
		op = p.parseGrammarTermValue()
	}
	bindings := make([]*ast.GrammarBindTerm, 0, 2)
	for {
		if p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].Kind == lexer.TOKEN_ASSIGN {
			bindPos := p.cur().Pos
			name := p.expect(lexer.TOKEN_IDENT).Text
			p.expect(lexer.TOKEN_ASSIGN)
			term := p.parseGrammarTermValue()
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
