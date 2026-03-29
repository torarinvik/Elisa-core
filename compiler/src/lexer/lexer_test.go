package lexer

import (
	"strings"
	"testing"
)

func TestTokenizeCharLiterals(t *testing.T) {
	l := New("chars.llcontext", []byte(`'a' '\n' '\x41' '\u0041' '\''`+"\n"))
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
			l := New(tc.name+".llcontext", []byte(tc.src))
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
