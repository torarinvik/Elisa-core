package lexer

import (
	"testing"
)

func collectTokens(src string) []Token {
	l := New("test.llcontext", []byte(src))
	return l.Tokenize()
}

func assertKinds(t *testing.T, got []Token, expected []TokenKind) {
	t.Helper()
	if len(got) != len(expected) {
		t.Errorf("expected %d tokens, got %d", len(expected), len(got))
		for i, tok := range got {
			t.Logf("  [%d] %s", i, tok)
		}
		return
	}
	for i := range expected {
		if got[i].Kind != expected[i] {
			t.Errorf("token[%d]: expected %s, got %s", i, tokenNames[expected[i]], got[i])
		}
	}
}

func TestSimpleIdent(t *testing.T) {
	tokens := collectTokens("hello\n")
	expect := []TokenKind{TOKEN_IDENT, TOKEN_NEWLINE, TOKEN_EOF}
	assertKinds(t, tokens, expect)
	if tokens[0].Text != "hello" {
		t.Errorf("expected text 'hello', got %q", tokens[0].Text)
	}
}

func TestKeyword(t *testing.T) {
	tokens := collectTokens("def foo:\n")
	expect := []TokenKind{TOKEN_DEF, TOKEN_IDENT, TOKEN_COLON, TOKEN_NEWLINE, TOKEN_EOF}
	assertKinds(t, tokens, expect)
}

func TestArrowOps(t *testing.T) {
	tokens := collectTokens("x <- y -> z\n")
	expect := []TokenKind{
		TOKEN_IDENT, TOKEN_LARROW, TOKEN_IDENT, TOKEN_ARROW, TOKEN_IDENT,
		TOKEN_NEWLINE, TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestComparison(t *testing.T) {
	tokens := collectTokens("a == b != c <= d >= e\n")
	expect := []TokenKind{
		TOKEN_IDENT, TOKEN_EQEQ, TOKEN_IDENT, TOKEN_BANGEQ, TOKEN_IDENT,
		TOKEN_LTEQ, TOKEN_IDENT, TOKEN_GTEQ, TOKEN_IDENT,
		TOKEN_NEWLINE, TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestAugmentedAssign(t *testing.T) {
	tokens := collectTokens("x += 1\ny -= 2\nz *= 3\nw ^= 4\n")
	expect := []TokenKind{
		TOKEN_IDENT, TOKEN_PLUSEQ, TOKEN_INT_LIT, TOKEN_NEWLINE,
		TOKEN_IDENT, TOKEN_MINUSEQ, TOKEN_INT_LIT, TOKEN_NEWLINE,
		TOKEN_IDENT, TOKEN_STAREQ, TOKEN_INT_LIT, TOKEN_NEWLINE,
		TOKEN_IDENT, TOKEN_CARETEQ, TOKEN_INT_LIT, TOKEN_NEWLINE,
		TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestNumberLiterals(t *testing.T) {
	tokens := collectTokens("42 0xff 123u64 0xABi32\n")
	expect := []TokenKind{
		TOKEN_INT_LIT, TOKEN_HEX_LIT, TOKEN_INT_LIT, TOKEN_HEX_LIT,
		TOKEN_NEWLINE, TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
	if tokens[2].Suffix != "u64" {
		t.Errorf("expected suffix 'u64', got %q", tokens[2].Suffix)
	}
	if tokens[3].Suffix != "i32" {
		t.Errorf("expected suffix 'i32', got %q", tokens[3].Suffix)
	}
}

func TestStringLiteral(t *testing.T) {
	tokens := collectTokens("\"hello world\"\n")
	expect := []TokenKind{TOKEN_STRING_LIT, TOKEN_NEWLINE, TOKEN_EOF}
	assertKinds(t, tokens, expect)
	if tokens[0].Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", tokens[0].Text)
	}
}

func TestIndentDedent(t *testing.T) {
	src := "def foo:\n    x <- 1\n    y <- 2\nz <- 3\n"
	tokens := collectTokens(src)
	expect := []TokenKind{
		TOKEN_DEF, TOKEN_IDENT, TOKEN_COLON, TOKEN_NEWLINE,
		TOKEN_INDENT,
		TOKEN_IDENT, TOKEN_LARROW, TOKEN_INT_LIT, TOKEN_NEWLINE,
		TOKEN_IDENT, TOKEN_LARROW, TOKEN_INT_LIT, TOKEN_NEWLINE,
		TOKEN_DEDENT,
		TOKEN_IDENT, TOKEN_LARROW, TOKEN_INT_LIT, TOKEN_NEWLINE,
		TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestNestedIndent(t *testing.T) {
	src := "if x:\n    if y:\n        z <- 1\n    w <- 2\nend\n"
	tokens := collectTokens(src)
	expect := []TokenKind{
		TOKEN_IF, TOKEN_IDENT, TOKEN_COLON, TOKEN_NEWLINE,
		TOKEN_INDENT,
		TOKEN_IF, TOKEN_IDENT, TOKEN_COLON, TOKEN_NEWLINE,
		TOKEN_INDENT,
		TOKEN_IDENT, TOKEN_LARROW, TOKEN_INT_LIT, TOKEN_NEWLINE,
		TOKEN_DEDENT,
		TOKEN_IDENT, TOKEN_LARROW, TOKEN_INT_LIT, TOKEN_NEWLINE,
		TOKEN_DEDENT,
		TOKEN_IDENT, TOKEN_NEWLINE,
		TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestComment(t *testing.T) {
	src := "# this is a comment\nx <- 1\n"
	tokens := collectTokens(src)
	expect := []TokenKind{TOKEN_IDENT, TOKEN_LARROW, TOKEN_INT_LIT, TOKEN_NEWLINE, TOKEN_EOF}
	assertKinds(t, tokens, expect)
}

func TestEllipsis(t *testing.T) {
	tokens := collectTokens("...\n")
	expect := []TokenKind{TOKEN_ELLIPSIS, TOKEN_NEWLINE, TOKEN_EOF}
	assertKinds(t, tokens, expect)
}

func TestRefType(t *testing.T) {
	tokens := collectTokens("Region& Arena&?\n")
	expect := []TokenKind{
		TOKEN_IDENT, TOKEN_AMPERSAND,
		TOKEN_IDENT, TOKEN_AMPERSAND, TOKEN_QUESTION,
		TOKEN_NEWLINE, TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestShiftOps(t *testing.T) {
	tokens := collectTokens("x << 3 >> 2\n")
	expect := []TokenKind{
		TOKEN_IDENT, TOKEN_LSHIFT, TOKEN_INT_LIT, TOKEN_RSHIFT, TOKEN_INT_LIT,
		TOKEN_NEWLINE, TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestParenSuppressesNewline(t *testing.T) {
	src := "foo(a,\n    b)\n"
	tokens := collectTokens(src)
	expect := []TokenKind{
		TOKEN_IDENT, TOKEN_LPAREN, TOKEN_IDENT, TOKEN_COMMA,
		TOKEN_IDENT, TOKEN_RPAREN, TOKEN_NEWLINE,
		TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}
