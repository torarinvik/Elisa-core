//go:build cgo

package backend

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

// BenchmarkIncrementalEditPipeline includes fresh parsing and native object emission.
// Stage metrics expose costs hidden by analysis-only benchmarks. It makes a
// novel body edit each iteration, preventing whole-revision cache hits. Linking
// and process startup are deliberately excluded; this is a synthetic workload.
func BenchmarkIncrementalEditPipeline(b *testing.B) {
	var source strings.Builder
	for i := 0; i < 256; i++ {
		term := fmt.Sprintf("(value%d * 2 + delta%d * 3)", i, i)
		fmt.Fprintf(&source, "def work%d(value%d: int, delta%d: int) -> int:\n    return %s\n\n", i, i, i, strings.Repeat(term+" + ", 15)+term)
	}
	base := source.String()
	parse := func(src string) *ast.File {
		l := lexer.New("pipeline_edit.elisa", []byte(src))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("pipeline_edit.elisa")
		if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("invalid pipeline workload")
		}
		return f
	}
	for _, mode := range []string{"fresh", "retained", "retained-audit", "incremental-native"} {
		b.Run(mode, func(b *testing.B) {
			retained := mode != "fresh"
			session := semantic.NewIncrementalAnalysisSessionWithOptions(semantic.IncrementalAnalysisSessionOptions{AuditLexicalReads: mode == "retained-audit"})
			analyze := func(f *ast.File) *semantic.Result {
				if retained {
					return session.Analyze(f, semantic.AnalyzeOptions{})
				}
				return semantic.Analyze(f)
			}
			warm := analyze(parse(base))
			if len(warm.Errors()) != 0 {
				b.Fatal(warm.Errors())
			}
			out := filepath.Join(b.TempDir(), "edit.o")
			objects := NewIncrementalObjectSession(8, 0)
			if mode == "incremental-native" {
				if err := objects.WriteObject(warm, out, LLVMObjectEmitOptions{}); err != nil {
					b.Fatal(err)
				}
			}
			objectBefore := objects.Stats()
			var parsing, analysis, lowering, emission time.Duration
			var reused, checked uint64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				src := strings.Replace(base, "value0 * 2", fmt.Sprintf("value0 * %d", i+4), 1)
				before := session.Stats()
				start := time.Now()
				f := parse(src)
				parsed := time.Now()
				r := analyze(f)
				analyzed := time.Now()
				if len(r.Errors()) != 0 {
					b.Fatal(r.Errors())
				}
				var lowerDuration, objectDuration time.Duration
				if mode == "incremental-native" {
					before := objects.Stats()
					if err := objects.WriteObject(r, out, LLVMObjectEmitOptions{}); err != nil {
						b.Fatal(err)
					}
					lowerDuration = objects.Stats().IRTime - before.IRTime
					objectDuration = time.Since(analyzed) - lowerDuration
				} else {
					g, err := compileLLVMModule(r, OptimizationLevel0, DefaultPackedLoweringProfile())
					lowered := time.Now()
					if err != nil {
						b.Fatal(err)
					}
					err = g.writeObjectFile(out, OptimizationLevel0)
					g.dispose()
					if err != nil {
						b.Fatal(err)
					}
					lowerDuration = lowered.Sub(analyzed)
					objectDuration = time.Since(lowered)
				}
				parsing += parsed.Sub(start)
				analysis += analyzed.Sub(parsed)
				lowering += lowerDuration
				emission += objectDuration
				after := session.Stats()
				reused += after.PrimaryBodiesReused - before.PrimaryBodiesReused
				checked += after.PrimaryBodiesChecked - before.PrimaryBodiesChecked
			}
			b.StopTimer()
			for key, elapsed := range map[string]time.Duration{"parse-ms/edit": parsing, "analysis-ms/edit": analysis, "lower-ms/edit": lowering, "object-ms/edit": emission} {
				b.ReportMetric(float64(elapsed)/float64(time.Millisecond)/float64(b.N), key)
			}
			if mode == "incremental-native" {
				after := objects.Stats()
				b.ReportMetric(float64(after.PartitionsEmitted-objectBefore.PartitionsEmitted)/float64(b.N), "emitted-parts/edit")
				b.ReportMetric(float64(after.PartitionsReused-objectBefore.PartitionsReused)/float64(b.N), "reused-parts/edit")
			}
			if retained {
				b.ReportMetric(float64(reused)/float64(b.N), "body-reuses/edit")
				b.ReportMetric(float64(checked)/float64(b.N), "body-checks/edit")
			}
		})
	}
}
