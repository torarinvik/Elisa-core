package lexer

import (
	"strings"
	"testing"
)

func TestTokenizeCharLiterals(t *testing.T) {
	l := New("chars.elisa", []byte(`'a' '\n' '\x41' '\u0041' '\''`+"\n"))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}

	var charTokens []Token
	for _, tok := range tokens {
		if tok.Kind == TOKEN_CHAR_LIT {
			charTokens = append(charTokens, tok)
		}
	}
	if len(charTokens) != 5 {
		t.Fatalf("expected 5 char tokens, got %d (%v)", len(charTokens), tokens)
	}
	want := []string{"a", "\n", "A", "A", "'"}
	for i, tok := range charTokens {
		if tok.Text != want[i] {
			t.Fatalf("char token %d text mismatch: got %q want %q", i, tok.Text, want[i])
		}
	}
	if tokens[len(tokens)-2].Kind != TOKEN_NEWLINE || tokens[len(tokens)-1].Kind != TOKEN_EOF {
		t.Fatalf("expected trailing newline and eof, got %v", tokens[len(tokens)-2:])
	}
}

func TestRejectInvalidCharLiterals(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "empty", src: "''\n", want: "empty char literal"},
		{name: "unterminated", src: "'a\n", want: "unterminated char literal"},
		{name: "invalid escape", src: "'\\q'\n", want: "invalid escape sequence \\\\q in char literal"},
		{name: "too wide ascii", src: "'ab'\n", want: "char literal must decode to exactly one code unit"},
		{name: "too wide unicode", src: "'\\u0080'\n", want: "char literal must decode to exactly one code unit"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := New(tc.name+".elisa", []byte(tc.src))
			_ = l.Tokenize()
			errs := l.Errors()
			if len(errs) == 0 {
				t.Fatalf("expected lexer error containing %q", tc.want)
			}
			if !strings.Contains(strings.Join(errs, "\n"), tc.want) {
				t.Fatalf("expected lexer errors to contain %q, got %v", tc.want, errs)
			}
		})
	}
}

func TestTokenizeRecordsTokenSpans(t *testing.T) {
	l := New("spans.elisa", []byte("alpha += 1\n"))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	if got := tokens[0].Pos.String(); got != "spans.elisa:1:1-6" {
		t.Fatalf("unexpected ident span: %s", got)
	}
	if got := tokens[1].Pos.String(); got != "spans.elisa:1:7-9" {
		t.Fatalf("unexpected += span: %s", got)
	}
	if got := tokens[2].Pos.String(); got != "spans.elisa:1:10-11" {
		t.Fatalf("unexpected int span: %s", got)
	}
	if !tokens[0].Pos.HasRange() {
		t.Fatal("expected identifier token to record a non-empty span")
	}
}

func TestTokenizeLayoutIntrospectionAliases(t *testing.T) {
	l := New("layout_aliases.elisa", []byte("size_of(Header) align_of(Header) offset_of(Header, count)\n"))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	want := []TokenKind{TOKEN_SIZEOF, TOKEN_ALIGNOF, TOKEN_OFFSETOF}
	gotIndex := 0
	for _, tok := range tokens {
		if tok.Kind == TOKEN_SIZEOF || tok.Kind == TOKEN_ALIGNOF || tok.Kind == TOKEN_OFFSETOF {
			if gotIndex >= len(want) {
				t.Fatalf("unexpected extra layout token %v", tok)
			}
			if tok.Kind != want[gotIndex] {
				t.Fatalf("layout token %d mismatch: got %v want %v", gotIndex, tok.Kind, want[gotIndex])
			}
			gotIndex++
		}
	}
	if gotIndex != len(want) {
		t.Fatalf("expected %d layout introspection tokens, got %d from %v", len(want), gotIndex, tokens)
	}
}

func TestTripleQuoteBlockComments(t *testing.T) {
	countKinds := func(src string) (strs []string, idents int, indents int, dedents int, errs int) {
		l := New("c.elisa", []byte(src))
		toks := l.Tokenize()
		errs = len(l.Errors())
		for _, tk := range toks {
			switch tk.Kind {
			case TOKEN_STRING_LIT:
				strs = append(strs, tk.Text)
			case TOKEN_IDENT:
				idents++
			case TOKEN_INDENT:
				indents++
			case TOKEN_DEDENT:
				dedents++
			}
		}
		return
	}

	// Single-line block comment produces no string/ident tokens and no errors.
	if s, id, _, _, e := countKinds(`"""just a comment"""` + "\n"); len(s) != 0 || id != 0 || e != 0 {
		t.Fatalf("single-line block comment: strs=%v idents=%d errs=%d", s, id, e)
	}

	// Multi-line block comment is fully consumed.
	multi := "\"\"\" a comment\nof multiple lines here\"\"\"\n"
	if s, id, _, _, e := countKinds(multi); len(s) != 0 || id != 0 || e != 0 {
		t.Fatalf("multi-line block comment: strs=%v idents=%d errs=%d", s, id, e)
	}

	// Normal string literals still lex correctly (not swallowed as comments).
	if s, _, _, _, e := countKinds(`x <- "hello"` + "\n"); e != 0 || len(s) != 1 || s[0] != "hello" {
		t.Fatalf("normal string regressed: strs=%v errs=%d", s, e)
	}

	// Empty string still lexes as a string, not a comment.
	if s, _, _, _, e := countKinds(`x <- ""` + "\n"); e != 0 || len(s) != 1 || s[0] != "" {
		t.Fatalf("empty string regressed: strs=%v errs=%d", s, e)
	}

	// A docstring-style block comment as the first line of a block must not disturb
	// indentation: the body still produces exactly one INDENT and its statement.
	body := "def f() -> void:\n    \"\"\"doc\nspanning\"\"\"\n    g()\n"
	s, id, indents, _, e := countKinds(body)
	if e != 0 {
		t.Fatalf("docstring body: unexpected errors=%d", e)
	}
	if len(s) != 0 {
		t.Fatalf("docstring body: unexpected string tokens %v", s)
	}
	if indents != 1 {
		t.Fatalf("docstring body: expected exactly 1 INDENT, got %d", indents)
	}
	// idents: f, void, g  -> at least g present
	if id < 1 {
		t.Fatalf("docstring body: expected body identifier, got idents=%d", id)
	}
}
