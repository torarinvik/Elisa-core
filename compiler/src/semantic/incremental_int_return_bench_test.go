//go:build cgo

package semantic

import (
	"context"
	"fmt"
	"runtime/pprof"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// Analysis-only body-edit workload: both paths parse fresh ASTs outside timing.
// Reuse counters distinguish typing avoidance from an unchanged object-cache hit.
func BenchmarkIncrementalIntReturnBodyEdit(b *testing.B) {
	var source strings.Builder
	for i := 0; i < 256; i++ {
		term := fmt.Sprintf("(value%d * 2 + delta%d * 3)", i, i)
		expression := strings.Repeat(term+" + ", 15) + term
		fmt.Fprintf(&source, "def work%d(value%d: int, delta%d: int) -> int:\n    return %s\n\n", i, i, i, expression)
	}
	base := source.String()
	edited := strings.Replace(base, "value0 * 2", "value0 * 4", 1)
	parse := func(source string) *ast.File {
		lex := lexer.New("int_edit_benchmark.elisa", []byte(source))
		p := parser.New(lex.Tokenize())
		file := p.ParseFile("int_edit_benchmark.elisa")
		if len(lex.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("invalid integer edit workload")
		}
		return file
	}
	for _, mode := range []string{"fresh", "retained"} {
		b.Run(mode, func(b *testing.B) {
			session := NewIncrementalAnalysisSession()
			if result := session.Analyze(parse(base), AnalyzeOptions{}); len(result.Errors()) != 0 {
				b.Fatal(result.Errors())
			}
			assertIncrementalResultParity(b, Analyze(parse(edited)), session.Analyze(parse(edited), AnalyzeOptions{}))
			b.ReportAllocs()
			b.ResetTimer()
			var reused, checked uint64
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				current := base
				if i%2 != 0 {
					current = edited
				}
				file := parse(current)
				before := session.Stats()
				var result *Result
				// Profile labels exclude parsing, prewarm and parity work even
				// though the Go profiler samples outside benchmark timers.
				b.StartTimer()
				pprof.Do(context.Background(), pprof.Labels("phase", "int-body-edit-analysis", "analysis-mode", mode), func(context.Context) {
					if mode == "retained" {
						result = session.Analyze(file, AnalyzeOptions{})
					} else {
						result = Analyze(file)
					}
				})
				b.StopTimer()
				if len(result.Errors()) != 0 {
					b.Fatal(result.Errors())
				}
				if mode == "retained" {
					after := session.Stats()
					if after.PrimaryBodiesChecked-before.PrimaryBodiesChecked != 1 || after.PrimaryBodiesReused-before.PrimaryBodiesReused != 255 {
						b.Fatalf("edit did not recheck exactly one body and reuse 255: before=%+v after=%+v", before, after)
					}
					reused += after.PrimaryBodiesReused - before.PrimaryBodiesReused
					checked += after.PrimaryBodiesChecked - before.PrimaryBodiesChecked
				}
			}
			if mode == "retained" {
				b.ReportMetric(float64(reused)/float64(b.N), "body-reuses/edit")
				b.ReportMetric(float64(checked)/float64(b.N), "body-checks/edit")
			}
		})
	}
}
