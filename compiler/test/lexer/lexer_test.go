package lexer_test

import (
	"llcontext/src/lexer"
	"strings"
	"testing"
)

func collectTokens(src string) []lexer.Token {
	l := lexer.New("test.llcontext", []byte(src))
	return l.Tokenize()
}

func collectTokensWithErrors(src string) ([]lexer.Token, []string) {
	l := lexer.New("test.llcontext", []byte(src))
	tokens := l.Tokenize()
	return tokens, l.Errors()
}

func assertKinds(t *testing.T, got []lexer.Token, expected []lexer.TokenKind) {
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
			t.Errorf("token[%d]: expected %s, got %s", i, lexer.TokenName(expected[i]), got[i])
		}
	}
}

func TestSimpleIdent(t *testing.T) {
	tokens := collectTokens("hello\n")
	expect := []lexer.TokenKind{lexer.TOKEN_IDENT, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF}
	assertKinds(t, tokens, expect)
	if tokens[0].Text != "hello" {
		t.Errorf("expected text 'hello', got %q", tokens[0].Text)
	}
}

func TestKeyword(t *testing.T) {
	tokens := collectTokens("def foo:\n")
	expect := []lexer.TokenKind{lexer.TOKEN_DEF, lexer.TOKEN_IDENT, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF}
	assertKinds(t, tokens, expect)
}

func TestArrowOps(t *testing.T) {
	tokens := collectTokens("x <- y -> z\n")
	expect := []lexer.TokenKind{
		lexer.TOKEN_IDENT, lexer.TOKEN_LARROW, lexer.TOKEN_IDENT, lexer.TOKEN_ARROW, lexer.TOKEN_IDENT,
		lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestComparison(t *testing.T) {
	tokens := collectTokens("a == b != c <= d >= e\n")
	expect := []lexer.TokenKind{
		lexer.TOKEN_IDENT, lexer.TOKEN_EQEQ, lexer.TOKEN_IDENT, lexer.TOKEN_BANGEQ, lexer.TOKEN_IDENT,
		lexer.TOKEN_LTEQ, lexer.TOKEN_IDENT, lexer.TOKEN_GTEQ, lexer.TOKEN_IDENT,
		lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestAugmentedAssign(t *testing.T) {
	tokens := collectTokens("x += 1\ny -= 2\nz *= 3\nr %= 5\nw ^= 4\n")
	expect := []lexer.TokenKind{
		lexer.TOKEN_IDENT, lexer.TOKEN_PLUSEQ, lexer.TOKEN_INT_LIT, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_IDENT, lexer.TOKEN_MINUSEQ, lexer.TOKEN_INT_LIT, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_IDENT, lexer.TOKEN_STAREQ, lexer.TOKEN_INT_LIT, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_IDENT, lexer.TOKEN_PERCENTEQ, lexer.TOKEN_INT_LIT, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_IDENT, lexer.TOKEN_CARETEQ, lexer.TOKEN_INT_LIT, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestModuloOps(t *testing.T) {
	tokens := collectTokens("x % 3\n")
	expect := []lexer.TokenKind{
		lexer.TOKEN_IDENT, lexer.TOKEN_PERCENT, lexer.TOKEN_INT_LIT,
		lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestNumberLiterals(t *testing.T) {
	tokens := collectTokens("42 0xff 123u64 0xABi32\n")
	expect := []lexer.TokenKind{
		lexer.TOKEN_INT_LIT, lexer.TOKEN_HEX_LIT, lexer.TOKEN_INT_LIT, lexer.TOKEN_HEX_LIT,
		lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
	if tokens[2].Suffix != "u64" {
		t.Errorf("expected suffix 'u64', got %q", tokens[2].Suffix)
	}
	if tokens[3].Suffix != "i32" {
		t.Errorf("expected suffix 'i32', got %q", tokens[3].Suffix)
	}
}

func TestFloatLiteralsAndDotCastBoundary(t *testing.T) {
	tokens := collectTokens("1.5 2e3 4.25f32 6f64 1. .5 1.e3 1.f32 0.char() 1...2\n")
	expect := []lexer.TokenKind{
		lexer.TOKEN_FLOAT_LIT, lexer.TOKEN_FLOAT_LIT, lexer.TOKEN_FLOAT_LIT, lexer.TOKEN_FLOAT_LIT,
		lexer.TOKEN_FLOAT_LIT, lexer.TOKEN_FLOAT_LIT, lexer.TOKEN_FLOAT_LIT, lexer.TOKEN_FLOAT_LIT,
		lexer.TOKEN_INT_LIT, lexer.TOKEN_DOT, lexer.TOKEN_IDENT, lexer.TOKEN_LPAREN, lexer.TOKEN_RPAREN,
		lexer.TOKEN_INT_LIT, lexer.TOKEN_ELLIPSIS, lexer.TOKEN_INT_LIT,
		lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
	if tokens[0].Text != "1.5" {
		t.Fatalf("expected first float text %q, got %q", "1.5", tokens[0].Text)
	}
	if tokens[1].Text != "2e3" {
		t.Fatalf("expected exponent float text %q, got %q", "2e3", tokens[1].Text)
	}
	if tokens[2].Suffix != "f32" {
		t.Fatalf("expected suffix %q, got %q", "f32", tokens[2].Suffix)
	}
	if tokens[3].Suffix != "f64" {
		t.Fatalf("expected suffix %q, got %q", "f64", tokens[3].Suffix)
	}
	if tokens[4].Text != "1." {
		t.Fatalf("expected trailing-decimal float text %q, got %q", "1.", tokens[4].Text)
	}
	if tokens[5].Text != ".5" {
		t.Fatalf("expected leading-decimal float text %q, got %q", ".5", tokens[5].Text)
	}
	if tokens[6].Text != "1.e3" {
		t.Fatalf("expected exponent-after-decimal float text %q, got %q", "1.e3", tokens[6].Text)
	}
	if tokens[7].Suffix != "f32" || tokens[7].Text != "1." {
		t.Fatalf("expected float with suffix text/suffix %q/%q, got %q/%q", "1.", "f32", tokens[7].Text, tokens[7].Suffix)
	}
}

func TestStringLiteral(t *testing.T) {
	tokens := collectTokens("\"hello world\"\n")
	expect := []lexer.TokenKind{lexer.TOKEN_STRING_LIT, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF}
	assertKinds(t, tokens, expect)
	if tokens[0].Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", tokens[0].Text)
	}
}

func TestStringLiteralDecodesEscapes(t *testing.T) {
	tokens, errs := collectTokensWithErrors("\"line\\n\\t\\\"\\\\\\0\"\n")
	if len(errs) != 0 {
		t.Fatalf("expected no lexer errors, got:\n%s", strings.Join(errs, "\n"))
	}
	expect := []lexer.TokenKind{lexer.TOKEN_STRING_LIT, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF}
	assertKinds(t, tokens, expect)
	want := []byte{'l', 'i', 'n', 'e', '\n', '\t', '"', '\\', 0}
	got := []byte(tokens[0].Text)
	if string(got) != string(want) {
		t.Fatalf("expected decoded bytes %v, got %v", want, got)
	}
}

func TestStringLiteralDecodesHexAndUnicodeEscapes(t *testing.T) {
	tokens, errs := collectTokensWithErrors("\"A\\x42\\u263A\"\n")
	if len(errs) != 0 {
		t.Fatalf("expected no lexer errors, got:\n%s", strings.Join(errs, "\n"))
	}
	expect := []lexer.TokenKind{lexer.TOKEN_STRING_LIT, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF}
	assertKinds(t, tokens, expect)
	if tokens[0].Text != "AB☺" {
		t.Fatalf("expected decoded string %q, got %q", "AB☺", tokens[0].Text)
	}
}

func TestStringLiteralReportsInvalidEscape(t *testing.T) {
	_, errs := collectTokensWithErrors("\"bad\\q\"\n")
	if len(errs) == 0 {
		t.Fatal("expected lexer error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "invalid escape sequence \\\\q in string literal") {
		t.Fatalf("expected invalid escape diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestStringLiteralReportsUnterminatedLiteral(t *testing.T) {
	tokens, errs := collectTokensWithErrors("\"bad\n")
	if len(errs) == 0 {
		t.Fatal("expected lexer error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "unterminated string literal") {
		t.Fatalf("expected unterminated string diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
	expect := []lexer.TokenKind{lexer.TOKEN_STRING_LIT, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF}
	assertKinds(t, tokens, expect)
}

func TestIndentDedent(t *testing.T) {
	src := "def foo:\n    x <- 1\n    y <- 2\nz <- 3\n"
	tokens := collectTokens(src)
	expect := []lexer.TokenKind{
		lexer.TOKEN_DEF, lexer.TOKEN_IDENT, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_INDENT,
		lexer.TOKEN_IDENT, lexer.TOKEN_LARROW, lexer.TOKEN_INT_LIT, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_IDENT, lexer.TOKEN_LARROW, lexer.TOKEN_INT_LIT, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_DEDENT,
		lexer.TOKEN_IDENT, lexer.TOKEN_LARROW, lexer.TOKEN_INT_LIT, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestNestedIndent(t *testing.T) {
	src := "if x:\n    if y:\n        z <- 1\n    w <- 2\nend\n"
	tokens := collectTokens(src)
	expect := []lexer.TokenKind{
		lexer.TOKEN_IF, lexer.TOKEN_IDENT, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_INDENT,
		lexer.TOKEN_IF, lexer.TOKEN_IDENT, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_INDENT,
		lexer.TOKEN_IDENT, lexer.TOKEN_LARROW, lexer.TOKEN_INT_LIT, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_DEDENT,
		lexer.TOKEN_IDENT, lexer.TOKEN_LARROW, lexer.TOKEN_INT_LIT, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_DEDENT,
		lexer.TOKEN_IDENT, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestComment(t *testing.T) {
	src := "# this is a comment\nx <- 1\n"
	tokens := collectTokens(src)
	expect := []lexer.TokenKind{lexer.TOKEN_IDENT, lexer.TOKEN_LARROW, lexer.TOKEN_INT_LIT, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF}
	assertKinds(t, tokens, expect)
}

func TestEllipsis(t *testing.T) {
	tokens := collectTokens("...\n")
	expect := []lexer.TokenKind{lexer.TOKEN_ELLIPSIS, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF}
	assertKinds(t, tokens, expect)
}

func TestRefType(t *testing.T) {
	tokens := collectTokens("any Region& any Arena&?\n")
	expect := []lexer.TokenKind{
		lexer.TOKEN_ANY, lexer.TOKEN_IDENT, lexer.TOKEN_AMPERSAND,
		lexer.TOKEN_ANY, lexer.TOKEN_IDENT, lexer.TOKEN_AMPERSAND, lexer.TOKEN_QUESTION,
		lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestStorageKeywordsAndCastTokens(t *testing.T) {
	tokens := collectTokens("heap Region&\nfoo.cast[stack i64&]()\n")
	expect := []lexer.TokenKind{
		lexer.TOKEN_HEAP, lexer.TOKEN_IDENT, lexer.TOKEN_AMPERSAND, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_IDENT, lexer.TOKEN_DOT, lexer.TOKEN_IDENT, lexer.TOKEN_LBRACKET,
		lexer.TOKEN_STACK, lexer.TOKEN_IDENT, lexer.TOKEN_AMPERSAND, lexer.TOKEN_RBRACKET,
		lexer.TOKEN_LPAREN, lexer.TOKEN_RPAREN, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestErrorHandlingKeywords(t *testing.T) {
	tokens := collectTokens("error IoError:\ndef foo() -> int error[IoError.NotFound, ...]:\n    try raise\n")
	expect := []lexer.TokenKind{
		lexer.TOKEN_ERROR, lexer.TOKEN_IDENT, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_DEF, lexer.TOKEN_IDENT, lexer.TOKEN_LPAREN, lexer.TOKEN_RPAREN,
		lexer.TOKEN_ARROW, lexer.TOKEN_IDENT, lexer.TOKEN_ERROR, lexer.TOKEN_LBRACKET,
		lexer.TOKEN_IDENT, lexer.TOKEN_DOT, lexer.TOKEN_IDENT, lexer.TOKEN_COMMA, lexer.TOKEN_ELLIPSIS, lexer.TOKEN_RBRACKET,
		lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_INDENT,
		lexer.TOKEN_TRY, lexer.TOKEN_RAISE, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_DEDENT,
		lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestErrorHandlingWildcardSyntax(t *testing.T) {
	tokens := collectTokens("def foo() -> int error[IoError, ...]:\n    pass\n")
	expect := []lexer.TokenKind{
		lexer.TOKEN_DEF, lexer.TOKEN_IDENT, lexer.TOKEN_LPAREN, lexer.TOKEN_RPAREN,
		lexer.TOKEN_ARROW, lexer.TOKEN_IDENT, lexer.TOKEN_ERROR, lexer.TOKEN_LBRACKET,
		lexer.TOKEN_IDENT, lexer.TOKEN_COMMA, lexer.TOKEN_ELLIPSIS, lexer.TOKEN_RBRACKET,
		lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_INDENT,
		lexer.TOKEN_PASS, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_DEDENT,
		lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestErrorHandlingMultiFamilySyntax(t *testing.T) {
	tokens := collectTokens("def foo() -> int error[IoError, NetworkError]:\n    pass\n")
	expect := []lexer.TokenKind{
		lexer.TOKEN_DEF, lexer.TOKEN_IDENT, lexer.TOKEN_LPAREN, lexer.TOKEN_RPAREN,
		lexer.TOKEN_ARROW, lexer.TOKEN_IDENT, lexer.TOKEN_ERROR, lexer.TOKEN_LBRACKET,
		lexer.TOKEN_IDENT, lexer.TOKEN_COMMA, lexer.TOKEN_IDENT, lexer.TOKEN_RBRACKET,
		lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_INDENT,
		lexer.TOKEN_PASS, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_DEDENT,
		lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestShiftOps(t *testing.T) {
	tokens := collectTokens("x << 3 >> 2\n")
	expect := []lexer.TokenKind{
		lexer.TOKEN_IDENT, lexer.TOKEN_LSHIFT, lexer.TOKEN_INT_LIT, lexer.TOKEN_RSHIFT, lexer.TOKEN_INT_LIT,
		lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}

func TestParenSuppressesNewline(t *testing.T) {
	src := "foo(a,\n    b)\n"
	tokens := collectTokens(src)
	expect := []lexer.TokenKind{
		lexer.TOKEN_IDENT, lexer.TOKEN_LPAREN, lexer.TOKEN_IDENT, lexer.TOKEN_COMMA,
		lexer.TOKEN_IDENT, lexer.TOKEN_RPAREN, lexer.TOKEN_NEWLINE,
		lexer.TOKEN_EOF,
	}
	assertKinds(t, tokens, expect)
}
