package parser

import "elisacore/src/lexer"

// Estimates are only capacity hints.  A malformed or delimiter-heavy source file must not be
// able to turn a missed closing token into a multi-gigabyte speculative allocation; the slices
// below still grow normally when the parser actually appends more items.
const (
	maxEstimatedItems = 1024
	// A capacity hint need only sample the start of a block. An outer block
	// can contain an arbitrarily large nested block that contributes one item.
	maxIndentedEstimateTokens = 1024
	// Nested tokens add no capacity to the enclosing block. Bound the
	// lookahead past an indent separately from the flat-item sample.
	maxNestedEstimateTokens = 96
)

func boundedEstimate(value int) int {
	if value < 0 {
		return 0
	}
	if value > maxEstimatedItems {
		return maxEstimatedItems
	}
	return value
}

func (p *Parser) estimateTopLevelItemCount() int {
	return boundedEstimate(p.estimateIndentedItemsFrom(p.pos, lexer.TOKEN_EOF))
}

func (p *Parser) estimateIndentedItemCount() int {
	return boundedEstimate(p.estimateIndentedItemsFrom(p.pos, lexer.TOKEN_DEDENT))
}

func (p *Parser) estimateIndentedItemsFrom(start int, stop lexer.TokenKind) int {
	if start >= len(p.tokens) {
		return 0
	}
	count := 0
	atLineStart := true
	end := start + min(len(p.tokens)-start, maxIndentedEstimateTokens)
	for i := start; i < end; i++ {
		switch p.tokens[i].Kind {
		case lexer.TOKEN_INDENT:
			depth := 1
			nestedEnd := i + min(end-i, maxNestedEstimateTokens)
			for i++; i < nestedEnd && depth != 0; i++ {
				switch p.tokens[i].Kind {
				case lexer.TOKEN_INDENT:
					depth++
				case lexer.TOKEN_DEDENT:
					depth--
				case lexer.TOKEN_EOF:
					return count
				}
			}
			if depth != 0 {
				return count
			}
			i--
			atLineStart = false
		case lexer.TOKEN_DEDENT:
			if stop == lexer.TOKEN_DEDENT {
				return count
			}
			atLineStart = false
		case lexer.TOKEN_EOF:
			return count
		case lexer.TOKEN_NEWLINE:
			atLineStart = true
		default:
			if atLineStart {
				count++
				atLineStart = false
			}
		}
	}
	return boundedEstimate(count)
}

func (p *Parser) estimateCommaSeparatedCount(close lexer.TokenKind) int {
	if p.peek() == close {
		return 0
	}
	count := 1
	depth := 0
	for i := p.pos; i < len(p.tokens); i++ {
		switch p.tokens[i].Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN:
			if close == lexer.TOKEN_RPAREN {
				if depth == 0 {
					return boundedEstimate(count)
				}
				depth--
			} else if depth > 0 {
				depth--
			}
		case lexer.TOKEN_RBRACKET:
			if close == lexer.TOKEN_RBRACKET {
				if depth == 0 {
					return boundedEstimate(count)
				}
				depth--
			} else if depth > 0 {
				depth--
			}
		case lexer.TOKEN_RBRACE:
			if close == lexer.TOKEN_RBRACE {
				if depth == 0 {
					return boundedEstimate(count)
				}
				depth--
			} else if depth > 0 {
				depth--
			}
		case lexer.TOKEN_COLON:
			if close == lexer.TOKEN_COLON && depth == 0 {
				return boundedEstimate(count)
			}
		case lexer.TOKEN_COMMA:
			if depth == 0 {
				count++
			}
		case lexer.TOKEN_EOF:
			return boundedEstimate(count)
		}
	}
	return boundedEstimate(count)
}
