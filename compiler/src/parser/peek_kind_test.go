package parser

import (
	"elisacore/src/lexer"
	"testing"
)

func TestPeekKindMatchesCurrentAtBoundaries(t *testing.T) {
	tokens := lexer.TokenizeSource("peek.elisa", []byte("def f() -> i64:\n    return 1\n"), nil).BorrowReadOnlyTokens()
	p := NewWithBorrowedTokens(tokens)
	for pos := 0; pos <= len(tokens)+1; pos++ {
		p.pos = pos
		if got, want := p.peek(), p.cur().Kind; got != want {
			t.Fatalf("position %d: peek=%v cur=%v", pos, got, want)
		}
	}
}
