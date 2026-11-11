package lexer

import (
	"unicode/utf8"
)

type Lexer struct {
	trackReads bool
	readLimit  int
	lineEpoch  int // increments on #line attribution resets
	src        []byte
	filename   string
	pos        int
	line       int
	col        int

	// Indentation tracking
	indentStack   []int
	atLineStart   bool
	pendingToks   []Token
	lastTokenKind TokenKind

	// Track paren/bracket depth to suppress INDENT/DEDENT inside groupings
	parenDepth           int
	pendingGroupedBlock  bool
	pendingPatternBlocks []groupedPatternHeader
	groupedBlockContexts []groupedBlockContext
	errors               []string
}
type groupedPatternHeader struct {
	ParenDepth int
	HeadStart  int
}

type groupedBlockContext struct {
	ParenDepth   int
	BaseIndent   int
	HeaderIndent int
}

func New(filename string, src []byte) *Lexer {
	recordParitySource(filename, src)
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
	// Nested ordinary parentheses still suppress layout; an explicit nested
	// block installs its own context at that deeper delimiter depth.
	return l.parenDepth == l.groupedBlockContexts[len(l.groupedBlockContexts)-1].ParenDepth
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

// Reuse the backing array after the final pending indentation token. Most
// lines enqueue a single token, so advancing the slice on every drain used to
// allocate a new array at the next indentation change.
func (l *Lexer) takePendingToken() Token {
	tok := l.pendingToks[0]
	if len(l.pendingToks) == 1 {
		l.pendingToks[0] = Token{}
		l.pendingToks = l.pendingToks[:0]
	} else {
		l.pendingToks = l.pendingToks[1:]
	}
	return tok
}
func (l *Lexer) peek() byte {
	if !l.sourceHas(l.pos) {
		return 0
	}
	return l.sourceByte(l.pos)
}
func (l *Lexer) peekRune() (rune, int) {
	if !l.sourceHas(l.pos) {
		return 0, 0
	}
	lookahead := l.runeBytes(l.pos)
	if lookahead[0] < utf8.RuneSelf {
		return rune(lookahead[0]), 1
	}
	r, size := utf8.DecodeRune(lookahead)
	if r == utf8.RuneError && size == 1 {
		return rune(lookahead[0]), 1
	}
	return r, size
}
func (l *Lexer) peekAt(offset int) byte {
	idx := l.pos + offset
	if !l.sourceHas(idx) {
		return 0
	}
	return l.sourceByte(idx)
}
func (l *Lexer) advance() byte {
	if !l.sourceHas(l.pos) {
		return 0
	}
	ch := l.sourceByte(l.pos)
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
	for l.sourceHas(l.pos) && l.peek() != '\n' {
		l.advance()
	}
}

// isBlockCommentStart reports whether the lexer is positioned at a triple
// double-quote (`"""`), which opens a multi-line comment.
func (l *Lexer) isBlockCommentStart() bool {
	return l.peek() == '"' && l.peekAt(1) == '"' && l.peekAt(2) == '"'
}

// skipBlockComment consumes a `""" ... """` multi-line comment. It must be called
// with the lexer positioned at the opening `"""`. The closing delimiter is the next
// `"""`; block comments do not nest. advance() keeps line/col accurate across the
// embedded newlines. An unterminated comment is consumed to EOF.
func (l *Lexer) skipBlockComment() {
	// Consume the opening `"""`.
	l.advance()
	l.advance()
	l.advance()
	for l.sourceHas(l.pos) {
		if l.isBlockCommentStart() {
			// Consume the closing `"""`.
			l.advance()
			l.advance()
			l.advance()
			return
		}
		l.advance()
	}
}

// tryConsumeLineDirective recognizes a `#line <num> <path>` directive (emitted by
// include expansion) and retargets the lexer's position to the real source file
// and line, so every token Pos attributes to the file the code actually came
// from rather than the flattened include buffer. Returns false for ordinary `#`
// comments, which are skipped normally. Called with l.peek() == '#'.
func (l *Lexer) tryConsumeLineDirective() bool {
	// Include expansion emits the canonical `#line` spelling. Do not accept
	// whitespace between `#` and `line`: ordinary documentation comments such as
	// `#   line 1: ...` must remain comments and must not reset the source position.
	i := l.pos + 1
	const kw = "line"
	if !l.sourceHas(i + len(kw) - 1) {
		return false
	}
	for k := 0; k < len(kw); k++ {
		if l.sourceByte(i+k) != kw[k] {
			return false
		}
	}
	j := i + len(kw)
	if !l.sourceHas(j) || (l.sourceByte(j) != ' ' && l.sourceByte(j) != '\t') {
		return false
	}
	for l.sourceHas(j) && (l.sourceByte(j) == ' ' || l.sourceByte(j) == '\t') {
		j++
	}
	numStart := j
	num := 0
	for l.sourceHas(j) && l.sourceByte(j) >= '0' && l.sourceByte(j) <= '9' {
		num = num*10 + int(l.sourceByte(j)-'0')
		j++
	}
	if j == numStart {
		return false
	}
	for l.sourceHas(j) && (l.sourceByte(j) == ' ' || l.sourceByte(j) == '\t') {
		j++
	}
	fileStart := j
	for l.sourceHas(j) && l.sourceByte(j) != '\n' {
		j++
	}
	fileEnd := j
	for fileEnd > fileStart && (l.sourceByte(fileEnd-1) == ' ' || l.sourceByte(fileEnd-1) == '\t' || l.sourceByte(fileEnd-1) == '\r') {
		fileEnd--
	}
	if fileEnd == fileStart {
		return false
	}
	file := string(l.sourceSlice(fileStart, fileEnd))
	if l.sourceHas(j) && l.sourceByte(j) == '\n' {
		j++
	}
	l.pos = j
	l.line = num
	l.lineEpoch++
	l.col = 1
	l.filename = file
	l.atLineStart = true
	return true
}
func (l *Lexer) measureIndent() int {
	indent := 0
	for l.sourceHas(l.pos) {
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
		if !l.sourceHas(l.pos) || l.peek() == '\n' {
			// Consuming a `#line` directive below sets atLineStart; clear it so it
			// does not leak out and trigger a spurious mid-line re-entry into
			// indentation handling on the next token.
			l.atLineStart = false
			return
		}
		if l.peek() == '#' {
			// A `#line <num> <path>` directive (always emitted at column 0 by
			// include expansion) must retarget position here too — otherwise the
			// comment-skip below would swallow it and lose the attribution.
			if indent == 0 && l.tryConsumeLineDirective() {
				indent = l.measureIndent()
				continue
			}
			// Skip comment and trailing newline, then re-measure
			l.skipLineComment()
			if l.sourceHas(l.pos) && l.peek() == '\n' {
				l.advance()
				// Skip consecutive blank lines
				for l.sourceHas(l.pos) && l.peek() == '\n' {
					l.advance()
				}
			}
			indent = l.measureIndent()
			continue
		}
		if l.isBlockCommentStart() {
			// A `""" ... """` block comment at line start: consume it (it may span
			// lines) so it is invisible to indentation, exactly like a `#` line.
			l.skipBlockComment()
			for l.sourceHas(l.pos) && (l.peek() == ' ' || l.peek() == '\t') {
				l.advance()
			}
			if !l.sourceHas(l.pos) || l.peek() == '\n' {
				// Comment-only line: skip the newline + blanks and re-measure the
				// next real line's indentation.
				if l.sourceHas(l.pos) && l.peek() == '\n' {
					l.advance()
					for l.sourceHas(l.pos) && l.peek() == '\n' {
						l.advance()
					}
				}
				indent = l.measureIndent()
				continue
			}
			// Trailing code after the comment on the same line: that code is the
			// line's first token, at the line's already-measured leading indent.
			break
		}
		break
	}

	// See note above: clear any atLineStart set by a consumed `#line` directive.
	l.atLineStart = false

	// Closing a grouped block may align with a header line whose indentation
	// was suppressed by the surrounding parentheses. Do not invent an INDENT
	// for that closing delimiter after emitting the block's DEDENT.
	if l.activeGroupedBlockContext() {
		context := l.groupedBlockContexts[len(l.groupedBlockContexts)-1]
		if indent <= context.HeaderIndent && indent > context.BaseIndent {
			indent = context.BaseIndent
		}
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
