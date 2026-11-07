package semantic

import (
	"strings"
	"testing"
)

func BenchmarkSMTSplitTopLevel(b *testing.B) {
	for _, tc := range []struct{ name, text string }{
		{"ordinary", strings.Repeat("(assert (>= (+ v_x 1) v_long_name))\n", 1000)},
		{"quoted", strings.Repeat("(assert (= |"+strings.Repeat("abcdef", 20)+"| 0))\n", 1000)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.text)))
			for i := 0; i < b.N; i++ {
				if _, ok := smtSplitTopLevel(tc.text); !ok {
					b.Fatal("invalid query")
				}
			}
		})
	}
}
