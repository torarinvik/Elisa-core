package parser

import "elisacore/src/lexer"

// All syntax-sensitive token accesses pass here. Capacity-only estimates keep
// their raw reads, since allocation hints do not affect syntax or diagnostics.
func (p *Parser) observeToken(index int) {
	if p.trace == nil {
		return
	}
	if index < p.readFrom {
		p.readFrom = index
	}
	if index >= p.readThrough {
		p.readThrough = index + 1
	}
	if index >= len(p.tokens) {
		p.readLength = true
	}
}
func (p *Parser) hasToken(index int) bool {
	p.observeToken(index)
	return index < len(p.tokens)
}
func (p *Parser) tokenAt(index int) lexer.Token {
	p.observeToken(index)
	return p.tokens[index]
}
func (p *Parser) tokenCount() int {
	if p.trace != nil {
		p.readLength = true
	}
	return len(p.tokens)
}
func (p *Parser) tokenSpan(start, end int) []lexer.Token {
	if end > start {
		p.observeToken(start)
		p.observeToken(end - 1)
	}
	return p.tokens[start:end]
}
func (p *Parser) rewriteTokenKind(index int, kind lexer.TokenKind) {
	p.observeToken(index)
	p.tokens[index].Kind = kind
	// Recovery mutates the token stream. No transition containing it is reusable.
	if p.trace != nil {
		p.trace.invalid = true
	}
}

// Preserve the clamped-position probe without depending on the whole length
// when the requested token exists. A missing token still records EOF/length.
func (p *Parser) tokenAtOrLast(index int) lexer.Token {
	p.observeToken(index)
	if index < len(p.tokens) {
		return p.tokens[index]
	}
	return p.tokens[len(p.tokens)-1]
}

// Cursor initialization and moves already observe the current slot, so cur and
// peek retain the original cold fast path. Offset probes observe separately.
func (p *Parser) cur() lexer.Token {
	if p.pos >= len(p.tokens) {
		return lexer.Token{Kind: lexer.TOKEN_EOF}
	}
	return p.tokens[p.pos]
}
func (p *Parser) peekAt(offset int) lexer.TokenKind {
	i := p.pos + offset
	p.observeToken(i)
	if i >= len(p.tokens) {
		return lexer.TOKEN_EOF
	}
	return p.tokens[i].Kind
}
func (p *Parser) advance() lexer.Token {
	tok := p.cur()
	if p.pos < len(p.tokens) {
		p.pos++
		p.observeToken(p.pos)
	}
	return tok
}

// All cursor changes observe their destination, so cur/peek can remain the
// original cold fast path. Restoring a speculative cursor never loses reads.
func (p *Parser) seek(index int) { p.pos = index; p.observeToken(index) }
