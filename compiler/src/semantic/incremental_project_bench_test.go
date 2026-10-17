//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"encoding/json"
	"os"
	"testing"
)

// BenchmarkIncrementalProjectAnalysis measures frontend analysis on an opt-in
// frozen real project. Parsing is excluded equally, but each iteration gets a
// fresh AST. Retained includes current dependency capture/publication costs.
// This measures analysis alone, not client edit-to-executable latency.
// SMT is disabled by default to isolate non-solver work. Set
// ELISA_PROJECT_BENCH_SMT=1 to include proof discharge in both paths;
// other CLI-specific options and loader authority are not reconstructed here.
func BenchmarkIncrementalProjectAnalysis(b *testing.B) {
	path := os.Getenv("ELISA_LOOKUP_QUERY_PROBE_SNAPSHOT")
	if path == "" {
		b.Skip("set frozen project snapshot")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	var snapshot struct{ Filename, Source string }
	if err = json.Unmarshal(data, &snapshot); err != nil {
		b.Fatal(err)
	}
	if snapshot.Filename == "" || snapshot.Source == "" {
		b.Fatal("empty frozen project")
	}
	options := AnalyzeOptions{EnableSMT: os.Getenv("ELISA_PROJECT_BENCH_SMT") == "1"}
	parse := func() *ast.File {
		lex := lexer.New(snapshot.Filename, []byte(snapshot.Source))
		p := parser.New(lex.Tokenize())
		file := p.ParseFile(snapshot.Filename)
		if len(lex.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("frozen syntax changed")
		}
		return file
	}
	for _, mode := range []string{"fresh", "retained"} {
		b.Run(mode, func(b *testing.B) {
			b.StopTimer()
			var session *IncrementalAnalysisSession
			expectedErrors := -1
			if mode == "retained" {
				session = NewIncrementalAnalysisSession()
				expectedErrors = len(session.Analyze(parse(), options).Errors())
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				file := parse()
				b.StartTimer()
				var result *Result
				if session != nil {
					result = session.Analyze(file, options)
				} else {
					result = AnalyzeWithOptions(file, options)
				}
				b.StopTimer()
				errors := len(result.Errors())
				if expectedErrors < 0 {
					expectedErrors = errors
				}
				if errors != expectedErrors {
					b.Fatalf("diagnostic count changed: %d -> %d", expectedErrors, errors)
				}
			}
			if session != nil {
				stats := session.Stats()
				b.ReportMetric(float64(stats.PrimaryBodiesReused)/float64(b.N), "body-reuses/op")
				b.ReportMetric(float64(stats.LexicalQueryMemoHits)/float64(b.N), "lookup-hits/op")
			}
			b.ReportMetric(float64(expectedErrors), "diagnostics")
			if options.EnableSMT {
				b.ReportMetric(1, "smt-enabled")
			} else {
				b.ReportMetric(0, "smt-enabled")
			}
		})
	}
}
