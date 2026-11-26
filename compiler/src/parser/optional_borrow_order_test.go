package parser

import (
	"elisacore/src/lexer"
	"strings"
	"testing"
)

// `T?&` is not a type: Elisa spells the optional borrow `T&?`. The parser names that fix rather
// than failing on the stray `&` with "expected ), got &".
func TestOptionalBorrowWrongSuffixOrderNamesTheFix(t *testing.T) {
	source := "def count(edges: darray[usize]?&) -> usize:\n    0\n"
	p := New(lexer.New("order.elisa", []byte(source)).Tokenize())
	p.ParseFile("order.elisa")
	errors := strings.Join(p.Errors(), "\n")
	if !strings.Contains(errors, "write `&?` for an optional borrow") {
		t.Fatalf("want the `&?` spelling named, got:\n%s", errors)
	}
	if strings.Contains(errors, "expected )") {
		t.Fatalf("want no cascade after the suffix error, got:\n%s", errors)
	}
}
