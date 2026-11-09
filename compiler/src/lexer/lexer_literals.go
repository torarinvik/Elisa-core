package lexer

import (
	"unicode/utf16"
	"unicode/utf8"
)

func (l *Lexer) readString() Token {
	p := l.curPos()
	l.advance() // consume opening "
	decoded := make([]byte, 0, 16)
	for l.sourceHas(l.pos) {
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
		if !l.sourceHas(l.pos) {
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
	for l.sourceHas(l.pos) {
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
		if !l.sourceHas(l.pos) {
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
		if !l.sourceHas(l.pos) || !isHexDigit(l.peek()) {
			return 0, false
		}
		value = (value << 4) | hexDigitValue(l.advance())
	}
	return value, true
}
func (l *Lexer) readNumber() Token {
	p := l.curPos()
	start := l.pos

	if l.peek() == '0' && l.sourceHas(l.pos+1) && (l.sourceByte(l.pos+1) == 'x' || l.sourceByte(l.pos+1) == 'X') {
		l.advance() // 0
		l.advance() // x
		for l.sourceHas(l.pos) {
			if isHexDigit(l.peek()) {
				l.advance()
				continue
			}
			// A separator belongs to the literal only between two hex digits.
			// Leave a leading, trailing, or repeated underscore for the next token.
			if l.peek() == '_' && l.pos > start+2 && isHexDigit(l.sourceByte(l.pos-1)) && isHexDigit(l.peekAt(1)) {
				l.advance()
				continue
			}
			break
		}
		text := bytesToStringView(l.sourceSlice(start, l.pos))
		suffix := l.readTypeSuffix()
		return l.finishToken(Token{Kind: TOKEN_HEX_LIT, Text: text, Pos: p, Suffix: suffix})
	}

	for l.sourceHas(l.pos) && isDigit(l.peek()) {
		l.advance()
	}
	isFloat := false
	if l.shouldReadFloatFraction() {
		isFloat = true
		l.advance()
		for l.sourceHas(l.pos) && isDigit(l.peek()) {
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
			for l.sourceHas(l.pos) && isDigit(l.peek()) {
				l.advance()
			}
		}
	}
	text := bytesToStringView(l.sourceSlice(start, l.pos))
	suffix := l.readTypeSuffix()
	if suffix == NumericSuffixF32 || suffix == NumericSuffixF64 {
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
func (l *Lexer) readTypeSuffix() NumericSuffix {
	// Read an optional recognized suffix: u8/u16/u32/u64/u, i8/i16/i32/i64,
	// usize/isize, or f32/f64.
	if !l.sourceHas(l.pos) {
		return NumericSuffixNone
	}
	ch := l.peek()
	if ch != 'u' && ch != 'i' && ch != 'f' {
		return NumericSuffixNone
	}
	// Check that this looks like a suffix not an identifier continuation preceded by whitespace
	// Suffixes are immediately attached: 123u64
	start := l.pos
	l.advance() // consume 'u' or 'i'

	// Read digits or "size"
	for l.sourceHas(l.pos) && (isDigit(l.peek()) || isAlpha(l.peek())) {
		l.advance()
	}
	suffix := bytesToStringView(l.sourceSlice(start, l.pos))

	// Validate known suffixes and store a small code in the token.
	switch suffix {
	case "u8":
		return NumericSuffixU8
	case "u16":
		return NumericSuffixU16
	case "u32":
		return NumericSuffixU32
	case "u64":
		return NumericSuffixU64
	case "u":
		return NumericSuffixU
	case "i8":
		return NumericSuffixI8
	case "i16":
		return NumericSuffixI16
	case "i32":
		return NumericSuffixI32
	case "i64":
		return NumericSuffixI64
	case "usize":
		return NumericSuffixUSize
	case "isize":
		return NumericSuffixISize
	case "f32":
		return NumericSuffixF32
	case "f64":
		return NumericSuffixF64
	default:
		// Not a valid suffix, rewind
		l.pos = start
		l.col -= len(suffix)
		return NumericSuffixNone
	}
}
