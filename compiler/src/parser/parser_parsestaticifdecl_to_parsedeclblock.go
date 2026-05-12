package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (p *Parser) parseStaticDecl() ast.Decl {
	pos := p.cur().Pos
	p.expect(lexer.TOKEN_STATIC)
	if p.peek() == lexer.TOKEN_DEF {
		return p.parseFuncDeclWithAnnotationsAndStatic(nil, true)
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "generate" {
		p.advance()
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		return &ast.StaticGenerateDecl{Position: pos, Body: p.parseStaticGenerateBlock()}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "assert" {
		p.advance()
		if p.match(lexer.TOKEN_COLON) {
			p.expectNewline()
			return &ast.StaticAssertBlockDecl{Position: pos, Assertions: p.parseStaticAssertItemBlock()}
		}
		cond := p.parseExpr()
		var msg ast.Expr
		if p.match(lexer.TOKEN_COMMA) {
			msg = p.parseExpr()
		}
		p.expectNewline()
		return &ast.StaticAssertDecl{Position: pos, Cond: cond, Message: msg}
	}
	p.expect(lexer.TOKEN_IF)
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()

	thenBlock := p.parseDeclBlock()

	elifs := make([]ast.StaticElifDecl, 0, 2)
	var elseBlock []ast.Decl

	for p.skipNewlines(); p.peek() == lexer.TOKEN_STATIC; p.skipNewlines() {
		saved := p.pos
		p.advance()
		if p.peek() == lexer.TOKEN_ELIF {
			p.advance()
			elifCond := p.parseExpr()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elifBody := p.parseDeclBlock()
			elifs = append(elifs, ast.StaticElifDecl{Position: p.tokens[saved].Pos, Cond: elifCond, Body: elifBody})
		} else if p.peek() == lexer.TOKEN_ELSE {
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elseBlock = p.parseDeclBlock()
			break
		} else {
			p.pos = saved
			break
		}
	}

	return &ast.StaticIfDecl{Position: pos, Cond: cond, Then: thenBlock, Elifs: elifs, Else: elseBlock}
}

func (p *Parser) parseStaticGenerateBlock() []ast.StaticGenerateStmt {
	p.expect(lexer.TOKEN_INDENT)
	items := make([]ast.StaticGenerateStmt, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		item := p.parseStaticGenerateStmt()
		if item != nil {
			items = append(items, item)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return items
}

func (p *Parser) parseStaticGenerateStmt() ast.StaticGenerateStmt {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "emit" {
		p.advance()
		return &ast.StaticGenerateEmitDecl{Position: pos, Tokens: p.collectStaticGenerateEmitTokens()}
	}
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "for" {
		p.advance()
		name := p.expect(lexer.TOKEN_IDENT).Text
		p.expect(lexer.TOKEN_IN)
		source := p.parseExpr()
		var filter ast.Expr
		if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "where" {
			p.advance()
			filter = p.parseExpr()
		}
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		return &ast.StaticGenerateForDecl{Position: pos, Name: name, Source: source, Filter: filter, Body: p.parseStaticGenerateBlock()}
	}
	if p.peek() == lexer.TOKEN_IF {
		p.advance()
		cond := p.parseExpr()
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		thenBlock := p.parseStaticGenerateBlock()
		elifs := make([]ast.StaticGenerateElifDecl, 0, 2)
		var elseBlock []ast.StaticGenerateStmt
		for p.skipNewlines(); p.peek() == lexer.TOKEN_ELIF || p.peek() == lexer.TOKEN_ELSE; p.skipNewlines() {
			if p.peek() == lexer.TOKEN_ELIF {
				elifPos := p.cur().Pos
				p.advance()
				elifCond := p.parseExpr()
				p.expect(lexer.TOKEN_COLON)
				p.expectNewline()
				elifs = append(elifs, ast.StaticGenerateElifDecl{Position: elifPos, Cond: elifCond, Body: p.parseStaticGenerateBlock()})
				continue
			}
			p.advance()
			p.expect(lexer.TOKEN_COLON)
			p.expectNewline()
			elseBlock = p.parseStaticGenerateBlock()
			break
		}
		return &ast.StaticGenerateIfDecl{Position: pos, Cond: cond, Then: thenBlock, Elifs: elifs, Else: elseBlock}
	}
	p.errorf("static generate only allows emit, for, and if statements, got %s", p.cur())
	p.advance()
	return nil
}

func (p *Parser) collectStaticGenerateEmitTokens() []lexer.Token {
	tokens := make([]lexer.Token, 0, 16)
	for p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF {
		tokens = append(tokens, p.cur())
		p.advance()
	}
	if p.match(lexer.TOKEN_NEWLINE) {
		tokens = append(tokens, lexer.Token{Kind: lexer.TOKEN_NEWLINE})
	}
	if p.peek() != lexer.TOKEN_INDENT {
		return tokens
	}
	depth := 0
	for p.peek() != lexer.TOKEN_EOF {
		tok := p.cur()
		tokens = append(tokens, tok)
		p.advance()
		switch tok.Kind {
		case lexer.TOKEN_INDENT:
			depth++
		case lexer.TOKEN_DEDENT:
			depth--
			if depth == 0 {
				return tokens
			}
		}
	}
	return tokens
}

func (p *Parser) parseStaticAssertItemBlock() []ast.StaticAssertItem {
	p.expect(lexer.TOKEN_INDENT)
	items := make([]ast.StaticAssertItem, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		pos := p.cur().Pos
		cond := p.parseExpr()
		var msg ast.Expr
		if p.match(lexer.TOKEN_COMMA) {
			msg = p.parseExpr()
		}
		p.expectNewline()
		items = append(items, ast.StaticAssertItem{Position: pos, Cond: cond, Message: msg})
	}
	p.expect(lexer.TOKEN_DEDENT)
	return items
}
func (p *Parser) parseDeclBlock() []ast.Decl {
	p.expect(lexer.TOKEN_INDENT)
	decls := make([]ast.Decl, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		decl := p.parseDecl()
		if decl != nil {
			decls = append(decls, decl)
		}
	}
	p.expect(lexer.TOKEN_DEDENT)
	return decls
}
