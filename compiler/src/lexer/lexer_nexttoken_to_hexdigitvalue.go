package lexer

import (
	"fmt"
	"unicode"
	"unsafe"
)

// NextToken returns the next token from the source.
func (l *Lexer) NextToken() Token {
	// Drain pending tokens first (INDENT/DEDENT)
	if len(l.pendingToks) > 0 {
		tok := l.pendingToks[0]
		l.pendingToks = l.pendingToks[1:]
		return tok
	}

	// At line start, handle indentation outside groupings, and also for grouped block expressions.
	if l.atLineStart {
		l.atLineStart = false
		if l.parenDepth == 0 || l.activeGroupedBlockContext() {
			l.handleIndentation()
			l.trimFinishedGroupedBlockContexts()
			if len(l.pendingToks) > 0 {
				tok := l.pendingToks[0]
				l.pendingToks = l.pendingToks[1:]
				return tok
			}
		}
	}

	// Skip spaces (not newlines)
	for l.pos < len(l.src) && (l.peek() == ' ' || l.peek() == '\t') {
		l.advance()
	}

	if l.pos >= len(l.src) {
		// Emit remaining DEDENT tokens at EOF
		if len(l.indentStack) > 1 {
			l.indentStack = l.indentStack[:len(l.indentStack)-1]
			return l.finishToken(Token{Kind: TOKEN_DEDENT, Pos: l.curPos()})
		}
		return l.finishToken(Token{Kind: TOKEN_EOF, Pos: l.curPos()})
	}

	ch := l.peek()
	if l.pendingGroupedBlock && ch != ':' {
		l.pendingGroupedBlock = false
	}

	// Newlines
	if ch == '\n' {
		p := l.curPos()
		l.advance()
		l.atLineStart = true
		// Skip consecutive blank lines
		for l.pos < len(l.src) && l.peek() == '\n' {
			l.advance()
		}
		if l.parenDepth > 0 && !l.activeGroupedBlockContext() {
			// Inside ordinary grouped expressions, skip newlines silently.
			return l.NextToken()
		}
		return l.finishToken(Token{Kind: TOKEN_NEWLINE, Pos: p})
	}

	// Comments
	if ch == '#' {
		l.skipLineComment()
		return l.NextToken()
	}

	// String literals
	if ch == '"' {
		return l.readString()
	}
	if ch == '\'' {
		return l.readChar()
	}

	// Numbers
	if isDigit(ch) || (ch == '.' && isDigit(l.peekAt(1))) {
		return l.readNumber()
	}

	// Identifiers and keywords
	if r, _ := l.peekRune(); isIdentStartRune(r) {
		return l.readIdent()
	}

	// Multi-character operators
	p := l.curPos()

	switch ch {
	case '<':
		l.advance()
		if l.match('-') {
			return l.finishToken(Token{Kind: TOKEN_LARROW, Text: "<-", Pos: p})
		}
		if l.match('<') {
			if l.match('=') {
				return l.finishToken(Token{Kind: TOKEN_LSHIFTEQ, Text: "<<=", Pos: p})
			}
			return l.finishToken(Token{Kind: TOKEN_LSHIFT, Text: "<<", Pos: p})
		}
		if l.match('=') {
			return l.finishToken(Token{Kind: TOKEN_LTEQ, Text: "<=", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_LT, Text: "<", Pos: p})

	case '>':
		l.advance()
		if l.match('>') {
			if l.match('=') {
				return l.finishToken(Token{Kind: TOKEN_RSHIFTEQ, Text: ">>=", Pos: p})
			}
			return l.finishToken(Token{Kind: TOKEN_RSHIFT, Text: ">>", Pos: p})
		}
		if l.match('=') {
			return l.finishToken(Token{Kind: TOKEN_GTEQ, Text: ">=", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_GT, Text: ">", Pos: p})

	case '=':
		l.advance()
		if l.match('>') {
			return l.finishToken(Token{Kind: TOKEN_FATARROW, Text: "=>", Pos: p})
		}
		if l.match('=') {
			return l.finishToken(Token{Kind: TOKEN_EQEQ, Text: "==", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_ASSIGN, Text: "=", Pos: p})

	case '!':
		l.advance()
		if l.match('=') {
			return l.finishToken(Token{Kind: TOKEN_BANGEQ, Text: "!=", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_BANG, Text: "!", Pos: p})

	case '+':
		l.advance()
		if l.match('=') {
			return l.finishToken(Token{Kind: TOKEN_PLUSEQ, Text: "+=", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_PLUS, Text: "+", Pos: p})

	case '-':
		l.advance()
		if l.match('>') {
			return l.finishToken(Token{Kind: TOKEN_ARROW, Text: "->", Pos: p})
		}
		if l.match('=') {
			return l.finishToken(Token{Kind: TOKEN_MINUSEQ, Text: "-=", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_MINUS, Text: "-", Pos: p})

	case '*':
		l.advance()
		if l.match('=') {
			return l.finishToken(Token{Kind: TOKEN_STAREQ, Text: "*=", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_STAR, Text: "*", Pos: p})

	case '/':
		l.advance()
		if l.match('=') {
			return l.finishToken(Token{Kind: TOKEN_SLASHEQ, Text: "/=", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_SLASH, Text: "/", Pos: p})

	case '%':
		l.advance()
		if l.match('=') {
			return l.finishToken(Token{Kind: TOKEN_PERCENTEQ, Text: "%=", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_PERCENT, Text: "%", Pos: p})

	case '^':
		l.advance()
		if l.match('=') {
			return l.finishToken(Token{Kind: TOKEN_CARETEQ, Text: "^=", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_CARET, Text: "^", Pos: p})

	case '|':
		l.advance()
		if l.match('>') {
			return l.finishToken(Token{Kind: TOKEN_PIPEGT, Text: "|>", Pos: p})
		}
		if l.match('=') {
			return l.finishToken(Token{Kind: TOKEN_PIPEEQ, Text: "|=", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_PIPE, Text: "|", Pos: p})

	case '&':
		l.advance()
		if l.match('=') {
			return l.finishToken(Token{Kind: TOKEN_AMPEQ, Text: "&=", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_AMPERSAND, Text: "&", Pos: p})

	case '.':
		l.advance()
		if l.peek() == '.' {
			if l.peekAt(1) == '.' {
				l.advance()
				l.advance()
				return l.finishToken(Token{Kind: TOKEN_ELLIPSIS, Text: "...", Pos: p})
			}
			l.advance()
			if l.match('<') {
				return l.finishToken(Token{Kind: TOKEN_RANGE_LT, Text: "..<", Pos: p})
			}
			if l.match('>') {
				return l.finishToken(Token{Kind: TOKEN_RANGE_GT, Text: "..>", Pos: p})
			}
			return l.finishToken(Token{Kind: TOKEN_RANGE, Text: "..", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_DOT, Text: ".", Pos: p})

	case ':':
		l.advance()
		if l.match(':') {
			l.pendingGroupedBlock = false
			return l.finishToken(Token{Kind: TOKEN_SCOPE, Text: "::", Pos: p})
		}
		if l.pendingGroupedBlock && l.parenDepth > 0 {
			l.groupedBlockContexts = append(l.groupedBlockContexts, groupedBlockContext{
				ParenDepth: l.parenDepth,
				BaseIndent: l.indentStack[len(l.indentStack)-1],
			})
		}
		l.pendingGroupedBlock = false
		return l.finishToken(Token{Kind: TOKEN_COLON, Text: ":", Pos: p})

	case ',':
		l.advance()
		return l.finishToken(Token{Kind: TOKEN_COMMA, Text: ",", Pos: p})

	case '(':
		l.advance()
		l.parenDepth++
		l.pendingGroupedBlock = false
		return l.finishToken(Token{Kind: TOKEN_LPAREN, Text: "(", Pos: p})

	case ')':
		l.advance()
		if l.parenDepth > 0 {
			l.parenDepth--
		}
		l.pendingGroupedBlock = false
		return l.finishToken(Token{Kind: TOKEN_RPAREN, Text: ")", Pos: p})

	case '[':
		l.advance()
		l.parenDepth++
		l.pendingGroupedBlock = false
		return l.finishToken(Token{Kind: TOKEN_LBRACKET, Text: "[", Pos: p})

	case ']':
		l.advance()
		if l.parenDepth > 0 {
			l.parenDepth--
		}
		l.pendingGroupedBlock = false
		return l.finishToken(Token{Kind: TOKEN_RBRACKET, Text: "]", Pos: p})

	case '{':
		l.advance()
		l.parenDepth++
		l.pendingGroupedBlock = false
		return l.finishToken(Token{Kind: TOKEN_LBRACE, Text: "{", Pos: p})

	case '}':
		l.advance()
		if l.parenDepth > 0 {
			l.parenDepth--
		}
		l.pendingGroupedBlock = false
		return l.finishToken(Token{Kind: TOKEN_RBRACE, Text: "}", Pos: p})

	case '?':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			return l.finishToken(Token{Kind: TOKEN_QASSIGN, Text: "?=", Pos: p})
		}
		return l.finishToken(Token{Kind: TOKEN_QUESTION, Text: "?", Pos: p})

	case '~':
		l.advance()
		return l.finishToken(Token{Kind: TOKEN_TILDE, Text: "~", Pos: p})

	case '@':
		l.advance()
		return l.finishToken(Token{Kind: TOKEN_AT, Text: "@", Pos: p})
	}

	// Unknown character
	l.advance()
	return l.finishToken(Token{Kind: TOKEN_IDENT, Text: string(ch), Pos: p})
}

// Tokenize returns all tokens from the source.
func (l *Lexer) Tokenize() []Token {
	tokens := make([]Token, 0, max(16, len(l.src)/4))
	for {
		tok := l.NextToken()
		tokens = append(tokens, tok)
		if tok.Kind == TOKEN_EOF {
			break
		}
	}
	return tokens
}
func bytesToStringView(src []byte) string {
	if len(src) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(src), len(src))
}
func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}
func isHexDigit(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}
func isAlpha(ch byte) bool {
	return unicode.IsLetter(rune(ch))
}
func isIdentStart(ch byte) bool {
	return ch == '_' || ch == '$' || isAlpha(ch)
}
func isIdentChar(ch byte) bool {
	return ch == '_' || ch == '$' || isAlpha(ch) || isDigit(ch)
}
func isIdentStartRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r)
}
func isIdentCharRune(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// Error helper
func (l *Lexer) errorf(format string, args ...interface{}) error {
	prefix := fmt.Sprintf("%s:%d:%d: ", l.filename, l.line, l.col)
	return fmt.Errorf(prefix+format, args...)
}
func (l *Lexer) reportErrorAt(pos Pos, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	l.errors = append(l.errors, fmt.Sprintf("%s: %s", pos, msg))
}
func hexDigitValue(ch byte) int {
	switch {
	case ch >= '0' && ch <= '9':
		return int(ch - '0')
	case ch >= 'a' && ch <= 'f':
		return int(ch-'a') + 10
	case ch >= 'A' && ch <= 'F':
		return int(ch-'A') + 10
	default:
		return 0
	}
}
