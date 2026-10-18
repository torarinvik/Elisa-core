package main

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

func BenchmarkSourceTokenKeyReuseEditedSource(b *testing.B) {
	// The large comment models a large loaded source without making semantic
	// analysis dominate this parse-cache benchmark. Source generation is untimed.
	original := "#" + strings.Repeat(" source", 1<<18) + "\n" + "def first() -> i64:\n    return 1\n\ndef middle() -> i64:\n    return 1000000\n\ndef last() -> i64:\n    return 3\n"
	inputs := make([][]byte, b.N)
	for i := range inputs {
		inputs[i] = []byte(strings.Replace(original, "return 1000000\n", fmt.Sprintf("return %d\n", 1000001+i), 1))
	}
	cache := newSourceTokenCache(8, 32<<20)
	for i := 0; i < 2; i++ {
		if _, ok := parseProgramWithTokenCache("keybench.elisa", []byte(original), io.Discard, cache); !ok {
			b.Fatal("prime")
		}
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(original)))
	b.ResetTimer()
	for _, source := range inputs {
		if _, ok := parseProgramWithTokenCache("keybench.elisa", source, io.Discard, cache); !ok {
			b.Fatal("parse")
		}
	}
}
