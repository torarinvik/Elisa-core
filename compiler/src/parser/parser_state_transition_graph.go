package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// This is a type-level relation. It deliberately creates no function declarations.
func (p *Parser) parseStateTransitionGraph() []ast.StateTransitionDecl {
	p.expectIdentText("transitions")
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	var edges []ast.StateTransitionDecl
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		from := p.expect(lexer.TOKEN_IDENT)
		p.expect(lexer.TOKEN_ARROW)
		to := p.expect(lexer.TOKEN_IDENT)
		edges = append(edges, ast.StateTransitionDecl{Position: from.Pos, From: from.Text, To: to.Text})
		p.expectNewline()
	}
	p.expect(lexer.TOKEN_DEDENT)
	return edges
}
