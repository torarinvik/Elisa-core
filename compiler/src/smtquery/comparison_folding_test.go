package smtquery

import (
	"elisacore/src/lexer"
	"testing"
)

func TestFoldCompareLargeAndSymbolicIntegers(t *testing.T) {
	for _, tc := range []struct {
		op                lexer.TokenKind
		left, right, want string
		ok                bool
	}{
		{lexer.TOKEN_GT, "18446744073709551616", "18446744073709551615", "true", true},
		{lexer.TOKEN_LTEQ, "(- 7)", "(- 7)", "true", true},
		{lexer.TOKEN_BANGEQ, "(- 8)", "(- 7)", "true", true},
		{lexer.TOKEN_EQEQ, "x", "0", "", false},
		{lexer.TOKEN_PLUS, "1", "2", "", false},
	} {
		if got, ok := FoldCompare(tc.op, tc.left, tc.right); got != tc.want || ok != tc.ok {
			t.Errorf("%v: got %q, %v", tc.op, got, ok)
		}
	}
}
