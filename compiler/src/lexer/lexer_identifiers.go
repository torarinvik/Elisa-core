package lexer

import (
	"unicode/utf8"
)

func (l *Lexer) readIdent() Token {
	p := l.curPos()
	start := l.pos
	for l.sourceHas(l.pos) {
		r, _ := l.peekRune()
		if !isIdentCharRune(r) {
			break
		}
		l.advanceRune()
	}
	text := bytesToStringView(l.sourceSlice(start, l.pos))
	kind := LookupKeyword(text)
	l.pendingGroupedBlock = kind == TOKEN_IDENT && isImmediateGroupedBlockIntroducer(text)
	if l.parenDepth > 0 && (kind == TOKEN_MATCH || (kind == TOKEN_IDENT && text == "when" && l.patternScrutineeFollows())) {
		l.pendingPatternBlocks = append(l.pendingPatternBlocks, groupedPatternHeader{ParenDepth: l.parenDepth, HeadStart: l.pos})
	}
	return l.finishToken(Token{Kind: kind, Text: text, Pos: p})
}

// readFString lexes an f-string literal `f"...{EXPR}..."` into a single TOKEN_FSTRING_LIT whose Text
// is the RAW interior (between the quotes, escapes and `{EXPR}` spans undecoded). The parser splits the
// interior into literal chunks and embedded expressions and desugars to a `__fstr(...)` builtin call —
// so the lexer only needs to find the closing quote. Scanning is brace- and string-aware: a `"` inside
// `{...}` belongs to an embedded string literal and does not terminate the f-string; `\"` in literal
// text is an escape (left undecoded here). Single-line only.
func (l *Lexer) readFString() Token {
	p := l.curPos()
	l.advance() // consume f
	l.advance() // consume opening "
	start := l.pos
	braceDepth := 0
	for l.sourceHas(l.pos) {
		ch := l.peek()
		if ch == '\n' {
			break
		}
		if braceDepth == 0 {
			switch ch {
			case '"':
				raw := bytesToStringView(l.sourceSlice(start, l.pos))
				l.advance() // consume closing "
				return l.finishToken(Token{Kind: TOKEN_FSTRING_LIT, Text: raw, Pos: p})
			case '\\':
				l.advance()
				if l.sourceHas(l.pos) && l.peek() != '\n' {
					l.advance()
				}
				continue
			case '{':
				// `{{` is a literal brace; a single `{` opens an embedded expression.
				if l.peekAt(1) == '{' {
					l.advance()
					l.advance()
					continue
				}
				braceDepth++
			case '}':
				if l.peekAt(1) == '}' {
					l.advance()
					l.advance()
					continue
				}
			}
			l.advance()
			continue
		}
		// Inside `{...}`: track nesting and skip embedded string literals wholesale.
		switch ch {
		case '{':
			braceDepth++
		case '}':
			braceDepth--
		case '"':
			l.advance()
			for l.sourceHas(l.pos) && l.peek() != '"' && l.peek() != '\n' {
				if l.peek() == '\\' {
					l.advance()
				}
				l.advance()
			}
		}
		l.advance()
	}
	l.reportErrorAt(p.WithEnd(l.curPos()), "unterminated f-string literal")
	return l.finishToken(Token{Kind: TOKEN_FSTRING_LIT, Text: bytesToStringView(l.sourceSlice(start, l.pos)), Pos: p})
}

func (l *Lexer) currentLineIndent() int {
	start := l.pos
	for start > 0 && l.sourceByte(start-1) != '\n' {
		start--
	}
	indent := 0
	for l.sourceHas(start) {
		switch l.sourceByte(start) {
		case ' ':
			indent++
		case '\t':
			indent += 4
		default:
			return indent
		}
		start++
	}
	return indent
}

// Mirror the parser's contextual `when` recognition. Postfix calls, indexing,
// fields and named arguments keep `when` as an ordinary identifier.
func (l *Lexer) patternScrutineeFollows() bool {
	index := l.pos
	for l.sourceHas(index) {
		switch l.sourceByte(index) {
		case ' ', '\t', '\r', '\n':
			index++
			continue
		case '#':
			for l.sourceHas(index) && l.sourceByte(index) != '\n' {
				index++
			}
			continue
		}
		break
	}
	if !l.sourceHas(index) {
		return false
	}
	ch := l.sourceByte(index)
	if ch == '-' || ch == '\'' || ch == '"' || (ch >= '0' && ch <= '9') || (ch == '.' && l.sourceHas(index+1) && isDigit(l.sourceByte(index+1))) {
		return true
	}
	r, size := utf8.DecodeRune(l.runeBytes(index))
	if !isIdentStartRune(r) {
		return false
	}
	start := index
	index += size
	for l.sourceHas(index) {
		r, size = utf8.DecodeRune(l.runeBytes(index))
		if !isIdentCharRune(r) {
			break
		}
		index += size
	}
	kind := LookupKeyword(bytesToStringView(l.sourceSlice(start, index)))
	return kind == TOKEN_IDENT || kind == TOKEN_TRUE || kind == TOKEN_FALSE
}
