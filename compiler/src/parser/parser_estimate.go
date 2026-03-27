package parser

import "llcontext/src/lexer"

func (p *Parser) estimateTopLevelItemCount() int {
	return p.estimateIndentedItemsFrom(p.pos, lexer.TOKEN_EOF)
}

func (p *Parser) estimateIndentedItemCount() int {
	return p.estimateIndentedItemsFrom(p.pos, lexer.TOKEN_DEDENT)
}

func (p *Parser) estimateIndentedItemsFrom(start int, stop lexer.TokenKind) int {
	if start >= len(p.tokens) {
		return 0
	}
	count := 0
	depth := 0
	atLineStart := true
	for i := start; i < len(p.tokens); i++ {
		switch p.tokens[i].Kind {
		case lexer.TOKEN_INDENT:
			depth++
			atLineStart = false
		case lexer.TOKEN_DEDENT:
			if depth == 0 && stop == lexer.TOKEN_DEDENT {
				return count
			}
			if depth > 0 {
				depth--
			}
			atLineStart = false
		case lexer.TOKEN_EOF:
			return count
		case lexer.TOKEN_NEWLINE:
			if depth == 0 {
				atLineStart = true
			}
		default:
			if depth == 0 && atLineStart {
				count++
				atLineStart = false
			}
		}
	}
	return count
}

func (p *Parser) estimateCommaSeparatedCount(close lexer.TokenKind) int {
	if p.peek() == close {
		return 0
	}
	count := 1
	depth := 0
	for i := p.pos; i < len(p.tokens); i++ {
		switch p.tokens[i].Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET:
			depth++
		case lexer.TOKEN_RPAREN:
			if close == lexer.TOKEN_RPAREN {
				if depth == 0 {
					return count
				}
				depth--
			} else if depth > 0 {
				depth--
			}
		case lexer.TOKEN_RBRACKET:
			if close == lexer.TOKEN_RBRACKET {
				if depth == 0 {
					return count
				}
				depth--
			} else if depth > 0 {
				depth--
			}
		case lexer.TOKEN_COMMA:
			if depth == 0 {
				count++
			}
		case lexer.TOKEN_EOF:
			return count
		}
	}
	return count
}
