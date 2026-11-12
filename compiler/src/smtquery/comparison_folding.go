package smtquery

import "elisacore/src/lexer"

func FoldCompare(op lexer.TokenKind, l, r string) (string, bool) {
	lv, rv, ok := IntPair(l, r)
	if !ok {
		return "", false
	}
	c := lv.Cmp(rv)
	var v bool
	switch op {
	case lexer.TOKEN_GT:
		v = c > 0
	case lexer.TOKEN_GTEQ:
		v = c >= 0
	case lexer.TOKEN_LT:
		v = c < 0
	case lexer.TOKEN_LTEQ:
		v = c <= 0
	case lexer.TOKEN_EQEQ:
		v = c == 0
	case lexer.TOKEN_BANGEQ:
		v = c != 0
	default:
		return "", false
	}
	if v {
		return "true", true
	}
	return "false", true
}
