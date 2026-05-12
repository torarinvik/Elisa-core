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
