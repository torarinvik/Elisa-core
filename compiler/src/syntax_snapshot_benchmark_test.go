package main

import (
	"elisacore/src/ast"
	"fmt"
	"io"
	"strings"
	"testing"
)

func BenchmarkSyntaxSnapshotCopy(b *testing.B) {
	for _, count := range []int{16, 256, 2048} {
		var source strings.Builder
		for i := 0; i < count; i++ {
			fmt.Fprintf(&source, "def work%d(x: i64) -> i64:\n    return x * 3 + 1\n\n", i)
		}
		file, ok := parseProgram("snapshot.elisa", []byte(source.String()), io.Discard)
		if !ok {
			b.Fatal("invalid source")
		}
		snapshot, err := ast.NewSyntaxSnapshot(file, 512<<20)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("capture%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := ast.NewSyntaxSnapshot(file, 512<<20); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("functions%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := snapshot.CopyFile(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
