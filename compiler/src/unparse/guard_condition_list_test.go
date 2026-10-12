package unparse_test

import (
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func TestControlGuardCanonicalFormatRoundTrip(t *testing.T) {
	src := "def probe(a: bool, b: bool) -> i64:\n    1 return if a\n    return 2 if:\n        a\n        b\n    return 3\n"
	l := lexer.New("guard.elisa", []byte(src))
	p := parser.New(l.Tokenize())
	file := p.ParseFile("guard.elisa")
	if len(p.Errors()) != 0 {
		t.Fatal(p.Errors())
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "return 1 if a") || !strings.Contains(formatted, "return 2 if:\n        a\n        b") {
		t.Fatalf("unexpected canonical format:\n%s", formatted)
	}
	l = lexer.New("guard.elisa", []byte(formatted))
	p = parser.New(l.Tokenize())
	file = p.ParseFile("guard.elisa")
	if len(p.Errors()) != 0 || len(p.Notices()) != 0 {
		t.Fatalf("format errors=%v notices=%v", p.Errors(), p.Notices())
	}
	if second := unparse.FormatFile(file); second != formatted {
		t.Fatalf("format is not stable:\n%s", second)
	}
}
