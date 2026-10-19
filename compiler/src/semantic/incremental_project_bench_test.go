//go:build cgo

package semantic

import (
	"context"
	"crypto/sha256"
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"encoding/json"
	"fmt"
	"os"
	"runtime/pprof"
	"slices"
	"sort"
	"strings"
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
	edits := os.Getenv("ELISA_PROJECT_BENCH_EDITS") == "1"
	if edits && strings.Contains(snapshot.Source, "__incremental_project_edit_probe") {
		b.Fatal("frozen source already declares the edit probe")
	}
	parse := func(revision int) *ast.File {
		source := snapshot.Source
		if edits {
			// Appending preserves all original diagnostic positions. Each timed
			// iteration gets a novel scalar body; the prewarm uses revision -1.
			source += fmt.Sprintf("\ndef __incremental_project_edit_probe() -> int:\n    return %d\n", revision+1)
		}
		lex := lexer.New(snapshot.Filename, []byte(source))
		p := parser.New(lex.Tokenize())
		file := p.ParseFile(snapshot.Filename)
		if len(lex.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("frozen syntax changed")
		}
		return file
	}
	var expectedDiagnostics []string
	checkDiagnostics := func(b *testing.B, result *Result) {
		b.Helper()
		current := make([]string, len(result.Diagnostics))
		for i, diagnostic := range result.Diagnostics {
			encoded, err := json.Marshal(diagnostic)
			if err != nil {
				b.Fatal(err)
			}
			current[i] = string(encoded)
		}
		sort.Strings(current)
		if expectedDiagnostics == nil {
			expectedDiagnostics = current
		} else if !slices.Equal(expectedDiagnostics, current) {
			b.Fatal("complete diagnostic multiset changed between project analyses")
		}
		encoded, _ := json.Marshal(current)
		b.Logf("diagnostic-multiset-sha256=%x entries=%d", sha256.Sum256(encoded), len(current))
	}
	for _, mode := range []string{"fresh", "retained"} {
		b.Run(mode, func(b *testing.B) {
			b.StopTimer()
			var session *IncrementalAnalysisSession
			var statsBefore IncrementalAnalysisStats
			expectedErrors := -1
			if mode == "retained" {
				session = NewIncrementalAnalysisSession()
				warm := session.Analyze(parse(-1), options)
				expectedErrors = len(warm.Errors())
				checkDiagnostics(b, warm)
				statsBefore = session.Stats()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				file := parse(i)
				var result *Result
				// Go's CPU profiler also samples code outside benchmark timers.
				// Labels isolate edited analysis from parsing, prewarm and parity checks.
				pprof.Do(context.Background(), pprof.Labels("phase", "project-analysis", "analysis-mode", mode), func(context.Context) {
					b.StartTimer()
					if session != nil {
						result = session.Analyze(file, options)
					} else {
						result = AnalyzeWithOptions(file, options)
					}
					b.StopTimer()
				})
				checkDiagnostics(b, result)
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
				// These distinguish missing reuse coverage from bounded-cache pressure.
				// Counters are deltas after prewarm, while retained bytes are a gauge.
				b.ReportMetric(float64(stats.ColdFallbackAnalyses-statsBefore.ColdFallbackAnalyses)/float64(b.N), "cold-fallbacks/op")
				b.ReportMetric(float64(stats.CapacityLimitedAnalyses-statsBefore.CapacityLimitedAnalyses)/float64(b.N), "capacity-limited/op")
				b.ReportMetric(float64(stats.CapacitySkippedQueries-statsBefore.CapacitySkippedQueries)/float64(b.N), "capacity-skips/op")
				b.ReportMetric(float64(stats.ProductAdmissionMisses-statsBefore.ProductAdmissionMisses)/float64(b.N), "product-misses/op")
				b.ReportMetric(float64(stats.BindingSpecAdmissionMisses-statsBefore.BindingSpecAdmissionMisses)/float64(b.N), "binding-misses/op")
				b.ReportMetric(float64(stats.RetainedProductBytes), "retained-product-bytes")
				b.ReportMetric(float64(stats.CapabilityProductsReused-statsBefore.CapabilityProductsReused)/float64(b.N), "capability-reuses/op")
				b.ReportMetric(float64(stats.CapabilityProjectionDeclines-statsBefore.CapabilityProjectionDeclines)/float64(b.N), "capability-declines/op")
				b.ReportMetric(float64(stats.PrimaryBodiesChecked-statsBefore.PrimaryBodiesChecked)/float64(b.N), "body-checks/op")
				b.ReportMetric(float64(stats.PrimaryBodiesReused-statsBefore.PrimaryBodiesReused)/float64(b.N), "body-reuses/op")
				b.ReportMetric(float64(stats.LocalInitializersReused-statsBefore.LocalInitializersReused)/float64(b.N), "local-init-reuses/op")
				b.ReportMetric(float64(stats.LocalInitializersChecked-statsBefore.LocalInitializersChecked)/float64(b.N), "local-init-checks/op")
				b.ReportMetric(float64(stats.ReusedExpressionNodes-statsBefore.ReusedExpressionNodes)/float64(b.N), "expr-nodes-reused/op")
				b.ReportMetric(float64(stats.LexicalQueryMemoHits-statsBefore.LexicalQueryMemoHits)/float64(b.N), "lookup-hits/op")
			}
			b.ReportMetric(float64(expectedErrors), "diagnostics")
			if edits {
				b.ReportMetric(1, "novel-body-edits/op")
			} else {
				b.ReportMetric(0, "novel-body-edits/op")
			}
			if options.EnableSMT {
				b.ReportMetric(1, "smt-enabled")
			} else {
				b.ReportMetric(0, "smt-enabled")
			}
		})
	}
}
