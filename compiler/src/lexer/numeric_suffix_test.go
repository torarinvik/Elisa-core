package lexer

import (
	"testing"
	"unsafe"
)

func TestNumericSuffixCodesPreserveSpellings(t *testing.T) {
	tests := []struct {
		text   string
		kind   TokenKind
		suffix NumericSuffix
	}{
		{"1u8", TOKEN_INT_LIT, NumericSuffixU8},
		{"1u16", TOKEN_INT_LIT, NumericSuffixU16},
		{"1u32", TOKEN_INT_LIT, NumericSuffixU32},
		{"1u64", TOKEN_INT_LIT, NumericSuffixU64},
		{"1u", TOKEN_INT_LIT, NumericSuffixU},
		{"1i8", TOKEN_INT_LIT, NumericSuffixI8},
		{"1i16", TOKEN_INT_LIT, NumericSuffixI16},
		{"1i32", TOKEN_INT_LIT, NumericSuffixI32},
		{"1i64", TOKEN_INT_LIT, NumericSuffixI64},
		{"1usize", TOKEN_INT_LIT, NumericSuffixUSize},
		{"1isize", TOKEN_INT_LIT, NumericSuffixISize},
		{"1f32", TOKEN_FLOAT_LIT, NumericSuffixF32},
		{"1f64", TOKEN_FLOAT_LIT, NumericSuffixF64},
	}
	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			lexer := New("suffix.elisa", []byte(tc.text+"\n"))
			tokens := lexer.Tokenize()
			if errors := lexer.Errors(); len(errors) != 0 {
				t.Fatalf("lexer errors: %v", errors)
			}
			if len(tokens) < 2 || tokens[0].Kind != tc.kind || tokens[0].Text != tc.text[:len(tc.text)-len(tc.suffix.String())] || tokens[0].Suffix != tc.suffix || tokens[0].SuffixString() != tc.suffix.String() || tokens[0].SuffixString() != tc.text[len(tc.text)-len(tc.suffix.String()):] {
				t.Fatalf("token = %#v; want kind %v and suffix %q", tokens, tc.kind, tc.suffix.String())
			}
		})
	}
}

func TestInvalidNumericSuffixRewindsToIdentifier(t *testing.T) {
	for _, text := range []string{"1u7", "1f128", "1usizex", "1I8"} {
		t.Run(text, func(t *testing.T) {
			lexer := New("suffix.elisa", []byte(text+"\n"))
			tokens := lexer.Tokenize()
			if errors := lexer.Errors(); len(errors) != 0 {
				t.Fatalf("lexer errors: %v", errors)
			}
			if len(tokens) < 3 || tokens[0].Kind != TOKEN_INT_LIT || tokens[0].Text != "1" || tokens[0].Suffix != NumericSuffixNone || tokens[1].Kind != TOKEN_IDENT || tokens[1].Text != text[1:] {
				t.Fatalf("tokens = %#v; want unsuffixed integer followed by identifier %q", tokens, text[1:])
			}
		})
	}
}

func TestTokenCompactLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof(Token{}) != 88 {
		t.Fatalf("Token size = %d; want 88 bytes on 64-bit targets", unsafe.Sizeof(Token{}))
	}
}

func TestInvalidNumericSuffixCodeIsNotTreatedAsAbsent(t *testing.T) {
	if got := (NumericSuffix(255)).String(); got == "" {
		t.Fatal("invalid numeric suffix code was treated as no suffix")
	}
}
