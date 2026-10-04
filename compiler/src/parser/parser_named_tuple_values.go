package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// namedTupleValueAhead distinguishes `(field: value, ...)` from a typed fold head
// `(acc: Type = seed, body for ... with acc = initial)`. A tuple value reaches a
// top-level comma or closing parenthesis before assignment; a fold binding reaches
// its top-level assignment first. Nested delimiters keep their punctuation local.
func (p *Parser) namedTupleValueAhead() bool {
	depth := 0
	for index := p.pos + 1; index < len(p.tokens); index++ {
		switch p.tokens[index].Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN:
			if depth == 0 {
				return true
			}
			depth--
		case lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_COMMA:
			if depth == 0 {
				return true
			}
		case lexer.TOKEN_ASSIGN:
			if depth == 0 {
				return false
			}
		}
	}
	return false
}

// parseNamedTupleExprFromFirst consumes the first field label after the caller
// has parsed it as an identifier. Labels are parser metadata in the self-hosted
// frontend; the Stage0 AST stores tuple values positionally, so preserve exactly
// the source order and discard only the labels.
func (p *Parser) parseNamedTupleExprFromFirst(pos lexer.Pos) ast.Expr {
	p.expect(lexer.TOKEN_COLON)
	elems := []ast.Expr{p.parseExpr()}
	for p.match(lexer.TOKEN_COMMA) {
		if p.peek() == lexer.TOKEN_RPAREN {
			break
		}
		if p.peek() == lexer.TOKEN_IDENT && p.peekAt(1) == lexer.TOKEN_COLON {
			p.advance()
			p.advance()
		}
		elems = append(elems, p.parseExpr())
	}
	p.expect(lexer.TOKEN_RPAREN)
	return &ast.TupleExpr{Position: pos, Elems: elems}
}
