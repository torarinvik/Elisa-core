package parsersyntax

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
	"strings"
)

func whenUnparen(e ast.Expr) ast.Expr {
	for {
		paren, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = paren.Inner
	}
}

// whenLiteralIntValue evaluates an int-or-char literal expression (with optional
// leading minus) to its value.
func WhenLiteralIntValue(e ast.Expr) (int64, bool) {
	switch v := whenUnparen(e).(type) {
	case *ast.IntLit:
		text := strings.ReplaceAll(v.Value, "_", "")
		base := 10
		if v.IsHex {
			text = strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X")
			base = 16
		}
		n, err := strconv.ParseInt(text, base, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	case *ast.CharLit:
		if r, ok := WhenCharValue(v.Value); ok {
			return int64(r), true
		}
		return 0, false
	case *ast.UnaryExpr:
		if v.Op != lexer.TOKEN_MINUS {
			return 0, false
		}
		n, ok := WhenLiteralIntValue(v.Operand)
		if !ok {
			return 0, false
		}
		return -n, true
	}
	return 0, false
}

// whenCharValue decodes a char-literal token body to its rune. Unknown escapes stay
// undecoded (the atom becomes opaque — a sound miss, never a wrong overlap).
func WhenCharValue(text string) (rune, bool) {
	runes := []rune(text)
	if len(runes) == 1 {
		return runes[0], true
	}
	if len(runes) == 2 && runes[0] == '\\' {
		switch runes[1] {
		case 'n':
			return '\n', true
		case 't':
			return '\t', true
		case 'r':
			return '\r', true
		case '0':
			return 0, true
		case '\\':
			return '\\', true
		case '\'':
			return '\'', true
		case '"':
			return '"', true
		}
	}
	return 0, false
}
