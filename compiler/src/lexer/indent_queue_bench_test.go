package lexer

import (
	"strings"
	"testing"
)

func BenchmarkTokenizeRepeatedIndentation(b *testing.B) {
	source := []byte(strings.Repeat("if true:\n    x = 1\ny = 2\n", 2000))
	b.ReportAllocs()
	b.SetBytes(int64(len(source)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := New("benchmark.elisa", source)
		if len(l.Tokenize()) == 0 || len(l.Errors()) != 0 {
			b.Fatal("invalid token stream")
		}
	}
}
