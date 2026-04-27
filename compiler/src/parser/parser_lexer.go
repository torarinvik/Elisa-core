package parser

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (p *Parser) parseLexerDecl() *ast.LexerDecl {
	pos := p.cur().Pos
	p.expectIdentText("lexer")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	var tokenKindType ast.TypeExpr
	var modeEnumName string
	modes := make([]ast.LexerModeDecl, 0)
	charClasses := make([]ast.LexerCharClassDecl, 0)
	var keywords *ast.LexerKeywordDecl
	var literals *ast.LexerLiteralDecl

	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		switch {
		case p.peekIdentText("token_kind"):
			p.expectIdentText("token_kind")
			tokenKindType = p.parseTypeExpr()
			p.expectNewline()
		case p.peekIdentText("mode_enum"):
			p.expectIdentText("mode_enum")
			modeEnumName = p.parseQualifiedDeclName()
			p.expectNewline()
		case p.peekIdentText("mode"):
			modes = append(modes, p.parseLexerModeDecl())
		case p.peekIdentText("charclass"):
			charClasses = append(charClasses, p.parseLexerCharClassDecl())
		case p.peekIdentText("keywords"):
			decl := p.parseLexerKeywordDecl()
			keywords = &decl
		case p.peekIdentText("literals"):
			decl := p.parseLexerLiteralDecl()
			literals = &decl
		default:
			p.errorf("expected lexer declaration item")
			p.skipLexerDeclLine()
		}
	}
	p.expect(lexer.TOKEN_DEDENT)

	return &ast.LexerDecl{Position: pos, Name: name, TokenKindType: tokenKindType, ModeEnumName: modeEnumName, Modes: modes, CharClasses: charClasses, Keywords: keywords, Literals: literals}
}

func (p *Parser) parseLexerModeDecl() ast.LexerModeDecl {
	pos := p.cur().Pos
	p.expectIdentText("mode")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expectNewline()
	return ast.LexerModeDecl{Position: pos, Name: name}
}

func (p *Parser) parseLexerCharClassDecl() ast.LexerCharClassDecl {
	pos := p.cur().Pos
	p.expectIdentText("charclass")
	name := p.expect(lexer.TOKEN_IDENT).Text
	p.expect(lexer.TOKEN_ASSIGN)
	terms := []ast.LexerCharClassTerm{p.parseLexerCharClassTerm()}
	for p.match(lexer.TOKEN_PIPE) {
		terms = append(terms, p.parseLexerCharClassTerm())
	}
	p.expectNewline()
	return ast.LexerCharClassDecl{Position: pos, Name: name, Terms: terms}
}

func (p *Parser) parseLexerCharClassTerm() ast.LexerCharClassTerm {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_CHAR_LIT {
		start := p.advance().Text
		term := ast.LexerCharClassTerm{Position: pos, Start: start}
		if p.match(lexer.TOKEN_RANGE) {
			term.Range = true
			term.End = p.expect(lexer.TOKEN_CHAR_LIT).Text
		}
		return term
	}
	if p.peek() == lexer.TOKEN_IDENT {
		return ast.LexerCharClassTerm{Position: pos, Name: p.advance().Text, Ref: true}
	}
	p.errorf("expected character literal or charclass name")
	p.advance()
	return ast.LexerCharClassTerm{Position: pos, Start: "\x00"}
}

func (p *Parser) parseLexerKeywordDecl() ast.LexerKeywordDecl {
	pos := p.cur().Pos
	p.expectIdentText("keywords")
	fallback := "IDENT"
	for p.peek() != lexer.TOKEN_COLON && p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF {
		if p.matchIdentText("fallback") {
			fallback = p.parseLexerKindName()
			continue
		}
		p.errorf("expected keywords option")
		p.advance()
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	entries := make([]ast.LexerKeywordEntry, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		entryPos := p.cur().Pos
		text := p.expect(lexer.TOKEN_STRING_LIT).Text
		p.expect(lexer.TOKEN_ARROW)
		kind := p.parseLexerKindName()
		p.expectNewline()
		entries = append(entries, ast.LexerKeywordEntry{Position: entryPos, Text: text, Kind: kind})
	}
	p.expect(lexer.TOKEN_DEDENT)
	return ast.LexerKeywordDecl{Position: pos, Fallback: fallback, Entries: entries}
}

func (p *Parser) parseLexerLiteralDecl() ast.LexerLiteralDecl {
	pos := p.cur().Pos
	p.expectIdentText("literals")
	fallback := "EOF"
	longest := false
	for p.peek() != lexer.TOKEN_COLON && p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_EOF {
		switch {
		case p.matchIdentText("fallback"):
			fallback = p.parseLexerKindName()
		case p.matchIdentText("longest"):
			longest = true
		default:
			p.errorf("expected literals option")
			p.advance()
		}
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	entries := make([]ast.LexerLiteralEntry, 0, p.estimateIndentedItemCount())
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		entryPos := p.cur().Pos
		text := p.expect(lexer.TOKEN_STRING_LIT).Text
		p.expect(lexer.TOKEN_ARROW)
		kind := p.parseLexerKindName()
		p.expectNewline()
		entries = append(entries, ast.LexerLiteralEntry{Position: entryPos, Text: text, Kind: kind})
	}
	p.expect(lexer.TOKEN_DEDENT)
	return ast.LexerLiteralDecl{Position: pos, Fallback: fallback, Longest: longest, Entries: entries}
}

func (p *Parser) parseLexerKindName() string {
	if p.match(lexer.TOKEN_DOT) {
		return p.expect(lexer.TOKEN_IDENT).Text
	}
	name := p.expect(lexer.TOKEN_IDENT).Text
	if p.match(lexer.TOKEN_DOT) {
		name = p.expect(lexer.TOKEN_IDENT).Text
	}
	return name
}

func (p *Parser) skipLexerDeclLine() {
	for p.peek() != lexer.TOKEN_NEWLINE && p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.advance()
	}
	if p.peek() == lexer.TOKEN_NEWLINE {
		p.advance()
	}
}
