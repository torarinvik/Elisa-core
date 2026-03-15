package lexer

import (
	"fmt"
	"unicode"
)

type Lexer struct {
	src      []byte
	filename string
	pos      int
	line     int
	col      int

	// Indentation tracking
	indentStack []int
	atLineStart bool
	pendingToks []Token

	// Track paren/bracket depth to suppress INDENT/DEDENT inside groupings
	parenDepth int
}

func New(filename string, src []byte) *Lexer {
	return &Lexer{
		src:         src,
		filename:    filename,
		pos:         0,
		line:        1,
		col:         1,
		indentStack: []int{0},
		atLineStart: true,
	}
}

func (l *Lexer) curPos() Pos {
	return Pos{File: l.filename, Line: l.line, Col: l.col, Offset: l.pos}
}

func (l *Lexer) peek() byte {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *Lexer) peekAt(offset int) byte {
	idx := l.pos + offset
	if idx >= len(l.src) {
		return 0
	}
	return l.src[idx]
}

func (l *Lexer) advance() byte {
	if l.pos >= len(l.src) {
		return 0
	}
	ch := l.src[l.pos]
	l.pos++
	if ch == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return ch
}

func (l *Lexer) match(expected byte) bool {
	if l.peek() == expected {
		l.advance()
		return true
	}
	return false
}

func (l *Lexer) skipLineComment() {
	for l.pos < len(l.src) && l.peek() != '\n' {
		l.advance()
	}
}

func (l *Lexer) measureIndent() int {
	indent := 0
	for l.pos < len(l.src) {
		ch := l.peek()
		if ch == ' ' {
			indent++
			l.advance()
		} else if ch == '\t' {
			indent += 4 // treat tab as 4 spaces
			l.advance()
		} else {
			break
		}
	}
	return indent
}

// handleIndentation processes beginning-of-line whitespace and emits INDENT/DEDENT tokens.
func (l *Lexer) handleIndentation() {
	indent := l.measureIndent()

	// Skip blank lines and comment-only lines (consume them entirely)
	for {
		if l.pos >= len(l.src) || l.peek() == '\n' {
			return
		}
		if l.peek() == '#' {
			// Skip comment and trailing newline, then re-measure
			l.skipLineComment()
			if l.pos < len(l.src) && l.peek() == '\n' {
				l.advance()
				// Skip consecutive blank lines
				for l.pos < len(l.src) && l.peek() == '\n' {
					l.advance()
				}
			}
			indent = l.measureIndent()
			continue
		}
		break
	}

	current := l.indentStack[len(l.indentStack)-1]

	if indent > current {
		l.indentStack = append(l.indentStack, indent)
		l.pendingToks = append(l.pendingToks, Token{
			Kind: TOKEN_INDENT,
			Pos:  l.curPos(),
		})
	} else if indent < current {
		for len(l.indentStack) > 1 && l.indentStack[len(l.indentStack)-1] > indent {
			l.indentStack = l.indentStack[:len(l.indentStack)-1]
			l.pendingToks = append(l.pendingToks, Token{
				Kind: TOKEN_DEDENT,
				Pos:  l.curPos(),
			})
		}
		if l.indentStack[len(l.indentStack)-1] != indent {
			l.pendingToks = append(l.pendingToks, Token{
				Kind: TOKEN_DEDENT,
				Pos:  l.curPos(),
			})
		}
	}
}

func (l *Lexer) readString() Token {
	p := l.curPos()
	l.advance() // consume opening "
	start := l.pos
	for l.pos < len(l.src) && l.peek() != '"' && l.peek() != '\n' {
		if l.peek() == '\\' {
			l.advance() // skip escape char
		}
		l.advance()
	}
	text := string(l.src[start:l.pos])
	if l.peek() == '"' {
		l.advance() // consume closing "
	}
	return Token{Kind: TOKEN_STRING_LIT, Text: text, Pos: p}
}

func (l *Lexer) readNumber() Token {
	p := l.curPos()
	start := l.pos

	if l.peek() == '0' && l.pos+1 < len(l.src) && (l.src[l.pos+1] == 'x' || l.src[l.pos+1] == 'X') {
		l.advance() // 0
		l.advance() // x
		for l.pos < len(l.src) && isHexDigit(l.peek()) {
			l.advance()
		}
		text := string(l.src[start:l.pos])
		suffix := l.readTypeSuffix()
		return Token{Kind: TOKEN_HEX_LIT, Text: text, Pos: p, Suffix: suffix}
	}

	for l.pos < len(l.src) && isDigit(l.peek()) {
		l.advance()
	}
	text := string(l.src[start:l.pos])
	suffix := l.readTypeSuffix()
	return Token{Kind: TOKEN_INT_LIT, Text: text, Pos: p, Suffix: suffix}
}

func (l *Lexer) readTypeSuffix() string {
	// Read optional type suffix: u8, u16, u32, u64, i8, i16, i32, i64, usize, isize, u, i
	if l.pos >= len(l.src) {
		return ""
	}
	ch := l.peek()
	if ch != 'u' && ch != 'i' {
		return ""
	}
	// Check that this looks like a suffix not an identifier continuation preceded by whitespace
	// Suffixes are immediately attached: 123u64
	start := l.pos
	l.advance() // consume 'u' or 'i'

	// Read digits or "size"
	for l.pos < len(l.src) && (isDigit(l.peek()) || isAlpha(l.peek())) {
		l.advance()
	}
	suffix := string(l.src[start:l.pos])

	// Validate known suffixes
	switch suffix {
	case "u8", "u16", "u32", "u64", "u",
		"i8", "i16", "i32", "i64",
		"usize", "isize":
		return suffix
	default:
		// Not a valid suffix, rewind
		l.pos = start
		l.col -= len(suffix)
		return ""
	}
}

func (l *Lexer) readIdent() Token {
	p := l.curPos()
	start := l.pos
	for l.pos < len(l.src) && isIdentChar(l.peek()) {
		l.advance()
	}
	text := string(l.src[start:l.pos])
	kind := LookupKeyword(text)
	return Token{Kind: kind, Text: text, Pos: p}
}

// NextToken returns the next token from the source.
func (l *Lexer) NextToken() Token {
	// Drain pending tokens first (INDENT/DEDENT)
	if len(l.pendingToks) > 0 {
		tok := l.pendingToks[0]
		l.pendingToks = l.pendingToks[1:]
		return tok
	}

	// At line start, handle indentation (only outside parens/brackets)
	if l.atLineStart {
		l.atLineStart = false
		if l.parenDepth == 0 {
			l.handleIndentation()
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
			return Token{Kind: TOKEN_DEDENT, Pos: l.curPos()}
		}
		return Token{Kind: TOKEN_EOF, Pos: l.curPos()}
	}

	ch := l.peek()

	// Newlines
	if ch == '\n' {
		p := l.curPos()
		l.advance()
		l.atLineStart = true
		// Skip consecutive blank lines
		for l.pos < len(l.src) && l.peek() == '\n' {
			l.advance()
		}
		if l.parenDepth > 0 {
			// Inside parens, skip newlines silently
			return l.NextToken()
		}
		return Token{Kind: TOKEN_NEWLINE, Pos: p}
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

	// Numbers
	if isDigit(ch) {
		return l.readNumber()
	}

	// Identifiers and keywords
	if isIdentStart(ch) {
		return l.readIdent()
	}

	// Multi-character operators
	p := l.curPos()

	switch ch {
	case '<':
		l.advance()
		if l.match('-') {
			return Token{Kind: TOKEN_LARROW, Text: "<-", Pos: p}
		}
		if l.match('<') {
			if l.match('=') {
				return Token{Kind: TOKEN_LSHIFTEQ, Text: "<<=", Pos: p}
			}
			return Token{Kind: TOKEN_LSHIFT, Text: "<<", Pos: p}
		}
		if l.match('=') {
			return Token{Kind: TOKEN_LTEQ, Text: "<=", Pos: p}
		}
		return Token{Kind: TOKEN_LT, Text: "<", Pos: p}

	case '>':
		l.advance()
		if l.match('>') {
			if l.match('=') {
				return Token{Kind: TOKEN_RSHIFTEQ, Text: ">>=", Pos: p}
			}
			return Token{Kind: TOKEN_RSHIFT, Text: ">>", Pos: p}
		}
		if l.match('=') {
			return Token{Kind: TOKEN_GTEQ, Text: ">=", Pos: p}
		}
		return Token{Kind: TOKEN_GT, Text: ">", Pos: p}

	case '=':
		l.advance()
		if l.match('=') {
			return Token{Kind: TOKEN_EQEQ, Text: "==", Pos: p}
		}
		return Token{Kind: TOKEN_ASSIGN, Text: "=", Pos: p}

	case '!':
		l.advance()
		if l.match('=') {
			return Token{Kind: TOKEN_BANGEQ, Text: "!=", Pos: p}
		}
		return Token{Kind: TOKEN_BANG, Text: "!", Pos: p}

	case '+':
		l.advance()
		if l.match('=') {
			return Token{Kind: TOKEN_PLUSEQ, Text: "+=", Pos: p}
		}
		return Token{Kind: TOKEN_PLUS, Text: "+", Pos: p}

	case '-':
		l.advance()
		if l.match('>') {
			return Token{Kind: TOKEN_ARROW, Text: "->", Pos: p}
		}
		if l.match('=') {
			return Token{Kind: TOKEN_MINUSEQ, Text: "-=", Pos: p}
		}
		return Token{Kind: TOKEN_MINUS, Text: "-", Pos: p}

	case '*':
		l.advance()
		if l.match('=') {
			return Token{Kind: TOKEN_STAREQ, Text: "*=", Pos: p}
		}
		return Token{Kind: TOKEN_STAR, Text: "*", Pos: p}

	case '/':
		l.advance()
		if l.match('=') {
			return Token{Kind: TOKEN_SLASHEQ, Text: "/=", Pos: p}
		}
		return Token{Kind: TOKEN_SLASH, Text: "/", Pos: p}

	case '^':
		l.advance()
		if l.match('=') {
			return Token{Kind: TOKEN_CARETEQ, Text: "^=", Pos: p}
		}
		return Token{Kind: TOKEN_CARET, Text: "^", Pos: p}

	case '|':
		l.advance()
		if l.match('=') {
			return Token{Kind: TOKEN_PIPEEQ, Text: "|=", Pos: p}
		}
		return Token{Kind: TOKEN_PIPE, Text: "|", Pos: p}

	case '&':
		l.advance()
		if l.match('=') {
			return Token{Kind: TOKEN_AMPEQ, Text: "&=", Pos: p}
		}
		return Token{Kind: TOKEN_AMPERSAND, Text: "&", Pos: p}

	case '.':
		l.advance()
		if l.peek() == '.' && l.peekAt(1) == '.' {
			l.advance()
			l.advance()
			return Token{Kind: TOKEN_ELLIPSIS, Text: "...", Pos: p}
		}
		return Token{Kind: TOKEN_DOT, Text: ".", Pos: p}

	case ':':
		l.advance()
		return Token{Kind: TOKEN_COLON, Text: ":", Pos: p}

	case ',':
		l.advance()
		return Token{Kind: TOKEN_COMMA, Text: ",", Pos: p}

	case '(':
		l.advance()
		l.parenDepth++
		return Token{Kind: TOKEN_LPAREN, Text: "(", Pos: p}

	case ')':
		l.advance()
		if l.parenDepth > 0 {
			l.parenDepth--
		}
		return Token{Kind: TOKEN_RPAREN, Text: ")", Pos: p}

	case '[':
		l.advance()
		l.parenDepth++
		return Token{Kind: TOKEN_LBRACKET, Text: "[", Pos: p}

	case ']':
		l.advance()
		if l.parenDepth > 0 {
			l.parenDepth--
		}
		return Token{Kind: TOKEN_RBRACKET, Text: "]", Pos: p}

	case '?':
		l.advance()
		return Token{Kind: TOKEN_QUESTION, Text: "?", Pos: p}

	case '~':
		l.advance()
		return Token{Kind: TOKEN_TILDE, Text: "~", Pos: p}

	case '@':
		l.advance()
		return Token{Kind: TOKEN_AT, Text: "@", Pos: p}
	}

	// Unknown character
	l.advance()
	return Token{Kind: TOKEN_IDENT, Text: string(ch), Pos: p}
}

// Tokenize returns all tokens from the source.
func (l *Lexer) Tokenize() []Token {
	var tokens []Token
	for {
		tok := l.NextToken()
		tokens = append(tokens, tok)
		if tok.Kind == TOKEN_EOF {
			break
		}
	}
	return tokens
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
	return ch == '_' || isAlpha(ch)
}

func isIdentChar(ch byte) bool {
	return ch == '_' || isAlpha(ch) || isDigit(ch)
}

// Error helper
func (l *Lexer) errorf(format string, args ...interface{}) error {
	prefix := fmt.Sprintf("%s:%d:%d: ", l.filename, l.line, l.col)
	return fmt.Errorf(prefix+format, args...)
}
