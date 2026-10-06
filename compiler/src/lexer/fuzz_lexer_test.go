package lexer

import (
	"testing"
	"time"

	"elisacore/internal/fuzzseed"
)

// FuzzLexer: the lexer must terminate on any byte string, never panic, end with exactly
// one EOF, and produce the same tokens when run twice.
func FuzzLexer(f *testing.F) {
	for _, s := range fuzzseed.Seeds() {
		f.Add(s)
	}
	fuzzseed.LimitStack()
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > fuzzseed.MaxInput {
			return
		}
		stop := fuzzseed.Watchdog(5*time.Second, "lexer", src)
		defer stop()
		toks := New("fuzz.elisa", src).Tokenize()
		if len(toks) == 0 || toks[len(toks)-1].Kind != TOKEN_EOF {
			t.Fatalf("token stream does not end in EOF")
		}
		for i, tok := range toks[:len(toks)-1] {
			if tok.Kind == TOKEN_EOF {
				t.Fatalf("EOF at %d before the end of the stream", i)
			}
		}
		again := New("fuzz.elisa", src).Tokenize()
		if len(again) != len(toks) {
			t.Fatalf("nondeterministic token count: %d vs %d", len(toks), len(again))
		}
		for i := range toks {
			if toks[i] != again[i] {
				t.Fatalf("nondeterministic token %d: %#v vs %#v", i, toks[i], again[i])
			}
		}
	})
}
