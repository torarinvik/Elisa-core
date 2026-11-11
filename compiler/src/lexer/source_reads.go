package lexer

import "unicode/utf8"

// A failed existence probe records the missing byte too. Appending after a
// previously observed EOF must invalidate a checkpoint that depended on it.
func (l *Lexer) noteRead(end int) {
	if l.trackReads && end > l.readLimit {
		l.readLimit = end
	}
}
func (l *Lexer) sourceHas(index int) bool          { l.noteRead(index + 1); return index < len(l.src) }
func (l *Lexer) sourceByte(index int) byte         { l.noteRead(index + 1); return l.src[index] }
func (l *Lexer) sourceSlice(start, end int) []byte { l.noteRead(end); return l.src[start:end] }
func (l *Lexer) runeBytes(index int) []byte {
	l.noteRead(index + utf8.UTFMax)
	return l.src[index:min(len(l.src), index+utf8.UTFMax)]
}

// sourceIdentifierEnd scans ASCII bytes in bulk and records one dependency for
// the whole span. Unicode uses the normal tracked rune lookahead.
func (l *Lexer) sourceIdentifierEnd(start int) int {
	end := start
	for end < len(l.src) {
		ch := l.src[end]
		if ch < utf8.RuneSelf {
			if !isASCIIIdentStart(ch) && !isDigit(ch) {
				break
			}
			end++
			continue
		}
		r, size := l.peekRuneAt(end)
		if !isIdentCharRune(r) {
			break
		}
		end += size
	}
	// Include the terminator or the failed EOF probe in the incremental
	// dependency. Multi-byte lookahead is recorded by peekRuneAt.
	l.noteRead(end + 1)
	return end
}
