package lexer

import (
	"unicode/utf16"
	"unicode/utf8"
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
	parenDepth           int
	pendingGroupedBlock  bool
	groupedBlockContexts []groupedBlockContext
	errors               []string
}
type groupedBlockContext struct {
	ParenDepth int
	BaseIndent int
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
func isImmediateGroupedBlockIntroducer(text string) bool {
	switch text {
	case "do":
		return true
	default:
		return false
	}
}
func (l *Lexer) activeGroupedBlockContext() bool {
	if len(l.groupedBlockContexts) == 0 {
		return false
	}
	return l.parenDepth >= l.groupedBlockContexts[len(l.groupedBlockContexts)-1].ParenDepth
}
func (l *Lexer) trimFinishedGroupedBlockContexts() {
	for len(l.groupedBlockContexts) > 0 {
		top := l.groupedBlockContexts[len(l.groupedBlockContexts)-1]
		currentIndent := l.indentStack[len(l.indentStack)-1]
		if currentIndent > top.BaseIndent {
			return
		}
		l.groupedBlockContexts = l.groupedBlockContexts[:len(l.groupedBlockContexts)-1]
	}
}
func (l *Lexer) curPos() Pos {
	return Pos{File: l.filename, Line: l.line, Col: l.col, Offset: l.pos}
}
func (l *Lexer) finishToken(tok Token) Token {
	tok.Pos = tok.Pos.WithEnd(l.curPos())
	return tok
}
func (l *Lexer) Errors() []string {
	if len(l.errors) == 0 {
		return nil
	}
	out := make([]string, len(l.errors))
	copy(out, l.errors)
	return out
}
func (l *Lexer) peek() byte {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}
func (l *Lexer) peekRune() (rune, int) {
	if l.pos >= len(l.src) {
		return 0, 0
	}
	r, size := utf8.DecodeRune(l.src[l.pos:])
	if r == utf8.RuneError && size == 1 {
		return rune(l.src[l.pos]), 1
	}
	return r, size
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
func (l *Lexer) advanceRune() rune {
	r, size := l.peekRune()
	if size == 0 {
		return 0
	}
	for i := 0; i < size; i++ {
		l.advance()
	}
	return r
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
		l.pendingToks = append(l.pendingToks, l.finishToken(Token{
			Kind: TOKEN_INDENT,
			Pos:  l.curPos(),
		}))
	} else if indent < current {
		for len(l.indentStack) > 1 && l.indentStack[len(l.indentStack)-1] > indent {
			l.indentStack = l.indentStack[:len(l.indentStack)-1]
			l.pendingToks = append(l.pendingToks, l.finishToken(Token{
				Kind: TOKEN_DEDENT,
				Pos:  l.curPos(),
			}))
		}
		if l.indentStack[len(l.indentStack)-1] < indent {
			l.indentStack = append(l.indentStack, indent)
			l.pendingToks = append(l.pendingToks, l.finishToken(Token{
				Kind: TOKEN_INDENT,
				Pos:  l.curPos(),
			}))
		}
	}
}
func (l *Lexer) readString() Token {
	p := l.curPos()
	l.advance() // consume opening "
	decoded := make([]byte, 0, 16)
	for l.pos < len(l.src) {
		ch := l.peek()
		if ch == '"' {
			l.advance()
			return l.finishToken(Token{Kind: TOKEN_STRING_LIT, Text: bytesToStringView(decoded), Pos: p})
		}
		if ch == '\n' {
			l.reportErrorAt(p.WithEnd(l.curPos()), "unterminated string literal")
			return l.finishToken(Token{Kind: TOKEN_STRING_LIT, Text: bytesToStringView(decoded), Pos: p})
		}
		if ch != '\\' {
			decoded = append(decoded, ch)
			l.advance()
			continue
		}

		escapePos := l.curPos()
		l.advance() // consume backslash
		if l.pos >= len(l.src) {
			l.reportErrorAt(escapePos.WithEnd(l.curPos()), "unterminated escape sequence in string literal")
			return l.finishToken(Token{Kind: TOKEN_STRING_LIT, Text: bytesToStringView(decoded), Pos: p})
		}

		esc := l.peek()
		switch esc {
		case '\\':
			decoded = append(decoded, '\\')
			l.advance()
		case '"':
			decoded = append(decoded, '"')
			l.advance()
		case 'n':
			decoded = append(decoded, '\n')
			l.advance()
		case 'r':
			decoded = append(decoded, '\r')
			l.advance()
		case 't':
			decoded = append(decoded, '\t')
			l.advance()
		case '0':
			decoded = append(decoded, 0)
			l.advance()
		case 'x':
			l.advance()
			value, ok := l.readFixedHexDigits(2)
			if !ok {
				l.reportErrorAt(escapePos.WithEnd(l.curPos()), "invalid hex escape in string literal")
				continue
			}
			decoded = append(decoded, byte(value))
		case 'u':
			l.advance()
			value, ok := l.readFixedHexDigits(4)
			if !ok {
				l.reportErrorAt(escapePos.WithEnd(l.curPos()), "invalid unicode escape in string literal")
				continue
			}
			if value >= 0xD800 && value <= 0xDBFF {
				pairPos := l.curPos()
				if l.peek() != '\\' || l.peekAt(1) != 'u' {
					l.reportErrorAt(escapePos.WithEnd(l.curPos()), "invalid unicode surrogate pair in string literal")
					continue
				}
				l.advance()
				l.advance()
				lowValue, ok := l.readFixedHexDigits(4)
				if !ok || lowValue < 0xDC00 || lowValue > 0xDFFF {
					l.reportErrorAt(pairPos.WithEnd(l.curPos()), "invalid unicode surrogate pair in string literal")
					continue
				}
				r := utf16.DecodeRune(rune(value), rune(lowValue))
				decoded = utf8.AppendRune(decoded, r)
				continue
			}
			if value >= 0xDC00 && value <= 0xDFFF {
				l.reportErrorAt(escapePos.WithEnd(l.curPos()), "invalid unicode surrogate pair in string literal")
				continue
			}
			decoded = utf8.AppendRune(decoded, rune(value))
		case '\n':
			l.reportErrorAt(p.WithEnd(l.curPos()), "unterminated string literal")
			return l.finishToken(Token{Kind: TOKEN_STRING_LIT, Text: bytesToStringView(decoded), Pos: p})
		default:
			l.reportErrorAt(escapePos.WithEnd(l.curPos()), "invalid escape sequence \\\\%c in string literal", esc)
			l.advance()
		}
	}

	l.reportErrorAt(p.WithEnd(l.curPos()), "unterminated string literal")
	return l.finishToken(Token{Kind: TOKEN_STRING_LIT, Text: bytesToStringView(decoded), Pos: p})
}
func (l *Lexer) readChar() Token {
	p := l.curPos()
	l.advance() // consume opening '
	decoded := make([]byte, 0, 4)
	invalid := false
	for l.pos < len(l.src) {
		ch := l.peek()
		if ch == '\'' {
			l.advance()
			if !invalid {
				switch len(decoded) {
				case 0:
					l.reportErrorAt(p.WithEnd(l.curPos()), "empty char literal")
				case 1:
					// OK.
				default:
					l.reportErrorAt(p.WithEnd(l.curPos()), "char literal must decode to exactly one code unit")
				}
			}
			return l.finishToken(Token{Kind: TOKEN_CHAR_LIT, Text: bytesToStringView(decoded), Pos: p})
		}
		if ch == '\n' {
			l.reportErrorAt(p.WithEnd(l.curPos()), "unterminated char literal")
			return l.finishToken(Token{Kind: TOKEN_CHAR_LIT, Text: bytesToStringView(decoded), Pos: p})
		}
		if ch != '\\' {
			decoded = append(decoded, ch)
			l.advance()
			continue
		}

		escapePos := l.curPos()
		l.advance() // consume backslash
		if l.pos >= len(l.src) {
			l.reportErrorAt(escapePos.WithEnd(l.curPos()), "unterminated escape sequence in char literal")
			return l.finishToken(Token{Kind: TOKEN_CHAR_LIT, Text: bytesToStringView(decoded), Pos: p})
		}

		esc := l.peek()
		switch esc {
		case '\\':
			decoded = append(decoded, '\\')
			l.advance()
		case '\'':
			decoded = append(decoded, '\'')
			l.advance()
		case '"':
			decoded = append(decoded, '"')
			l.advance()
		case 'n':
			decoded = append(decoded, '\n')
			l.advance()
		case 'r':
			decoded = append(decoded, '\r')
			l.advance()
		case 't':
			decoded = append(decoded, '\t')
			l.advance()
		case '0':
			decoded = append(decoded, 0)
			l.advance()
		case 'x':
			l.advance()
			value, ok := l.readFixedHexDigits(2)
			if !ok {
				l.reportErrorAt(escapePos.WithEnd(l.curPos()), "invalid hex escape in char literal")
				invalid = true
				continue
			}
			decoded = append(decoded, byte(value))
		case 'u':
			l.advance()
			value, ok := l.readFixedHexDigits(4)
			if !ok {
				l.reportErrorAt(escapePos.WithEnd(l.curPos()), "invalid unicode escape in char literal")
				invalid = true
				continue
			}
			if value >= 0xD800 && value <= 0xDBFF {
				pairPos := l.curPos()
				if l.peek() != '\\' || l.peekAt(1) != 'u' {
					l.reportErrorAt(escapePos.WithEnd(l.curPos()), "invalid unicode surrogate pair in char literal")
					invalid = true
					continue
				}
				l.advance()
				l.advance()
				lowValue, ok := l.readFixedHexDigits(4)
				if !ok || lowValue < 0xDC00 || lowValue > 0xDFFF {
					l.reportErrorAt(pairPos.WithEnd(l.curPos()), "invalid unicode surrogate pair in char literal")
					invalid = true
					continue
				}
				r := utf16.DecodeRune(rune(value), rune(lowValue))
				decoded = utf8.AppendRune(decoded, r)
				continue
			}
			if value >= 0xDC00 && value <= 0xDFFF {
				l.reportErrorAt(escapePos.WithEnd(l.curPos()), "invalid unicode surrogate pair in char literal")
				invalid = true
				continue
			}
			decoded = utf8.AppendRune(decoded, rune(value))
		case '\n':
			l.reportErrorAt(p.WithEnd(l.curPos()), "unterminated char literal")
			return l.finishToken(Token{Kind: TOKEN_CHAR_LIT, Text: bytesToStringView(decoded), Pos: p})
		default:
			l.reportErrorAt(escapePos.WithEnd(l.curPos()), "invalid escape sequence \\\\%c in char literal", esc)
			invalid = true
			l.advance()
		}
	}

	l.reportErrorAt(p.WithEnd(l.curPos()), "unterminated char literal")
	return l.finishToken(Token{Kind: TOKEN_CHAR_LIT, Text: bytesToStringView(decoded), Pos: p})
}
func (l *Lexer) readFixedHexDigits(count int) (int, bool) {
	value := 0
	for i := 0; i < count; i++ {
		if l.pos >= len(l.src) || !isHexDigit(l.peek()) {
			return 0, false
		}
		value = (value << 4) | hexDigitValue(l.advance())
	}
	return value, true
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
		text := bytesToStringView(l.src[start:l.pos])
		suffix := l.readTypeSuffix()
		return l.finishToken(Token{Kind: TOKEN_HEX_LIT, Text: text, Pos: p, Suffix: suffix})
	}

	for l.pos < len(l.src) && isDigit(l.peek()) {
		l.advance()
	}
	isFloat := false
	if l.shouldReadFloatFraction() {
		isFloat = true
		l.advance()
		for l.pos < len(l.src) && isDigit(l.peek()) {
			l.advance()
		}
	}
	if l.peek() == 'e' || l.peek() == 'E' {
		offset := 1
		if next := l.peekAt(offset); next == '+' || next == '-' {
			offset++
		}
		if isDigit(l.peekAt(offset)) {
			isFloat = true
			l.advance()
			if l.peek() == '+' || l.peek() == '-' {
				l.advance()
			}
			for l.pos < len(l.src) && isDigit(l.peek()) {
				l.advance()
			}
		}
	}
	text := bytesToStringView(l.src[start:l.pos])
	suffix := l.readTypeSuffix()
	if suffix == "f32" || suffix == "f64" {
		isFloat = true
	}
	if isFloat {
		return l.finishToken(Token{Kind: TOKEN_FLOAT_LIT, Text: text, Pos: p, Suffix: suffix})
	}
	return l.finishToken(Token{Kind: TOKEN_INT_LIT, Text: text, Pos: p, Suffix: suffix})
}
func (l *Lexer) shouldReadFloatFraction() bool {
	if l.peek() != '.' || l.peekAt(1) == '.' {
		return false
	}
	next := l.peekAt(1)
	if isDigit(next) || next == 0 {
		return true
	}
	if next == 'e' || next == 'E' {
		offset := 2
		if sign := l.peekAt(offset); sign == '+' || sign == '-' {
			offset++
		}
		return isDigit(l.peekAt(offset))
	}
	if l.hasFloatSuffixAt(1) {
		return true
	}
	return !isIdentChar(next)
}
func (l *Lexer) hasFloatSuffixAt(offset int) bool {
	if l.peekAt(offset) != 'f' {
		return false
	}
	if l.peekAt(offset+1) != '3' {
		return l.peekAt(offset+1) == '6' && l.peekAt(offset+2) == '4' && !isIdentChar(l.peekAt(offset+3))
	}
	return l.peekAt(offset+2) == '2' && !isIdentChar(l.peekAt(offset+3))
}
func (l *Lexer) readTypeSuffix() string {
	// Read optional type suffix: u8, u16, u32, u64, i8, i16, i32, i64, usize, isize, u, i, f32, f64
	if l.pos >= len(l.src) {
		return ""
	}
	ch := l.peek()
	if ch != 'u' && ch != 'i' && ch != 'f' {
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
	suffix := bytesToStringView(l.src[start:l.pos])

	// Validate known suffixes
	switch suffix {
	case "u8", "u16", "u32", "u64", "u",
		"i8", "i16", "i32", "i64",
		"usize", "isize",
		"f32", "f64":
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
	for l.pos < len(l.src) {
		r, _ := l.peekRune()
		if !isIdentCharRune(r) {
			break
		}
		l.advanceRune()
	}
	text := bytesToStringView(l.src[start:l.pos])
	kind := LookupKeyword(text)
	l.pendingGroupedBlock = kind == TOKEN_IDENT && isImmediateGroupedBlockIntroducer(text)
	return l.finishToken(Token{Kind: kind, Text: text, Pos: p})
}
