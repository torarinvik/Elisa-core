package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

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
		case lexer.TOKEN_COMMA, lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_NEWLINE, lexer.TOKEN_DEDENT, lexer.TOKEN_EOF:
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
