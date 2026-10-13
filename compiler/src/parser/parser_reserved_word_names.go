package parser

import "elisacore/src/lexer"

// Reserved keywords used as names (`to: i64 = 3`, `def f(stack: i64)`, a field named `tail`) used to
// produce "expected IDENT, got to" plus a cascade. The parser now names the actual problem at the
// site and then treats the word as an identifier so the rest of the line parses normally. Which words
// are reserved is unchanged.

// statementNameLikeReservedWords are keywords that read naturally as names. A statement that starts
// with one of these followed by `:`+type, `=`, or `<-` is a declaration/assignment of a reserved
// name. (Other keywords are left to their statement parsers.)
var statementNameLikeReservedWords = map[string]bool{
	"to": true, "stack": true, "heap": true, "tail": true, "error": true, "packed": true,
}

func (p *Parser) reportReservedWordAsName(tok lexer.Token) {
	p.errorAt(tok.Pos, "`%s` is a reserved keyword and cannot be used as a name", tok.Text)
}

// isReservedWordToken reports a keyword token spelled like an identifier.
func isReservedWordToken(tok lexer.Token) bool {
	if tok.Kind == lexer.TOKEN_IDENT || tok.Text == "" || !lexer.IsReservedWord(tok.Text) {
		return false
	}
	c := tok.Text[0]
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// recoverReservedWordStatementName rewrites a statement-initial `kw: T = …` / `kw = …` / `kw <- …`
// whose keyword is name-like into an identifier after reporting it.
func (p *Parser) recoverReservedWordStatementName() {
	tok := p.cur()
	if !statementNameLikeReservedWords[tok.Text] || !isReservedWordToken(tok) || p.pos+1 >= len(p.tokens) {
		return
	}
	next := p.tokens[p.pos+1]
	switch next.Kind {
	case lexer.TOKEN_COLON:
		if p.pos+2 >= len(p.tokens) || p.tokens[p.pos+2].Kind == lexer.TOKEN_NEWLINE {
			return // `kw:` block opener — not a name
		}
	case lexer.TOKEN_ASSIGN, lexer.TOKEN_LARROW:
	default:
		return
	}
	p.reportReservedWordAsName(tok)
	p.tokens[p.pos].Kind = lexer.TOKEN_IDENT
}

// recoverReservedWordLoopBinder handles `for to in xs:` / `for tail, x in …`; idx is the binder.
func (p *Parser) recoverReservedWordLoopBinder(idx int) {
	if idx+1 >= len(p.tokens) || !isReservedWordToken(p.tokens[idx]) {
		return
	}
	if next := p.tokens[idx+1].Kind; next != lexer.TOKEN_IN && next != lexer.TOKEN_COMMA {
		return
	}
	p.reportReservedWordAsName(p.tokens[idx])
	p.tokens[idx].Kind = lexer.TOKEN_IDENT
}
