package lexer

import "testing"

func TestHexSeparatorsStayBetweenDigits(t *testing.T) {
	tests := []struct {
		source string
		text   string
		suffix string
		tail   string
	}{
		{"0xffff_ffffu64", "0xffff_ffff", "u64", ""},
		{"0xF_f_01", "0xF_f_01", "", ""},
		{"0x_f", "0x", "", "_f"},
		{"0xf_", "0xf", "", "_"},
		{"0xf__f", "0xf", "", "__f"},
		{"0x1_g", "0x1", "", "_g"},
	}
	for _, tc := range tests {
		t.Run(tc.source, func(t *testing.T) {
			l := New("hex.elisa", []byte(tc.source+"\n"))
			tokens := l.Tokenize()
			if errors := l.Errors(); len(errors) != 0 {
				t.Fatalf("lexer errors: %v", errors)
			}
			if len(tokens) < 2 || tokens[0].Kind != TOKEN_HEX_LIT || tokens[0].Text != tc.text || tokens[0].SuffixString() != tc.suffix {
				t.Fatalf("first token = %#v; want hex %q suffix %q", tokens, tc.text, tc.suffix)
			}
			if tc.tail == "" {
				if tokens[1].Kind != TOKEN_NEWLINE {
					t.Fatalf("unexpected token after literal: %#v", tokens[1])
				}
			} else if tokens[1].Kind != TOKEN_IDENT || tokens[1].Text != tc.tail {
				t.Fatalf("token after literal = %#v; want identifier %q", tokens[1], tc.tail)
			}
		})
	}
}

func TestHexSeparatorIncrementalLexMatchesCold(t *testing.T) {
	prefix := editPreamble() + "const value = "
	var previous *SourceTokens
	for _, spelling := range []string{"0x7_", "0x7_f", "0x7__f", "0x7_f", "0x7_g", "0x7_1u64"} {
		previous = assertSourceTokensCold(t, prefix+spelling+"\n", previous)
	}
}
