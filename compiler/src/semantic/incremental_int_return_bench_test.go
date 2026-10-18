//go:build cgo

package semantic

import (
	"fmt"
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
		b.StartTimer()
		result := session.Analyze(file, AnalyzeOptions{})
		b.StopTimer()
		if len(result.Errors()) != 0 {
			b.Fatal(result.Errors())
		}
		after := session.Stats()
		reused += after.PrimaryBodiesReused - before.PrimaryBodiesReused
		checked += after.PrimaryBodiesChecked - before.PrimaryBodiesChecked
	}
	b.ReportMetric(float64(reused)/float64(b.N), "body-reuses/edit")
	b.ReportMetric(float64(checked)/float64(b.N), "body-checks/edit")
}
