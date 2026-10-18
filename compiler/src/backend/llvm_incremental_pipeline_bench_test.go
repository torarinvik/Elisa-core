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
// Stage metrics expose costs hidden by analysis-only benchmarks. It alternates an
// actual body edit, rather than repeatedly compiling an unchanged input. Linking
// and process startup are deliberately excluded; this is a synthetic workload.
func BenchmarkIncrementalEditPipeline(b *testing.B) {
	var source strings.Builder
	for i := 0; i < 256; i++ {
		term := fmt.Sprintf("(value%d * 2 + delta%d * 3)", i, i)
		fmt.Fprintf(&source, "def work%d(value%d: int, delta%d: int) -> int:\n    return %s\n\n", i, i, i, strings.Repeat(term+" + ", 15)+term)
	}
	base := source.String()
	edited := strings.Replace(base, "value0 * 2", "value0 * 4", 1)
	parse := func(src string) *ast.File {
		l := lexer.New("pipeline_edit.elisa", []byte(src))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("pipeline_edit.elisa")
		if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("invalid pipeline workload")
		}
		return f
	}
	for _, mode := range []string{"fresh", "retained", "retained-audit"} {
		b.Run(mode, func(b *testing.B) {
			retained := mode != "fresh"
			session := semantic.NewIncrementalAnalysisSessionWithOptions(semantic.IncrementalAnalysisSessionOptions{AuditLexicalReads: mode == "retained-audit"})
			analyze := func(f *ast.File) *semantic.Result {
				if retained {
					return session.Analyze(f, semantic.AnalyzeOptions{})
				}
				return semantic.Analyze(f)
			}
			if r := analyze(parse(edited)); len(r.Errors()) != 0 {
				b.Fatal(r.Errors())
			}
			out := filepath.Join(b.TempDir(), "edit.o")
			var parsing, analysis, lowering, emission time.Duration
			var reused, checked uint64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				src := base
				if i%2 != 0 {
					src = edited
				}
				before := session.Stats()
				start := time.Now()
				f := parse(src)
				parsed := time.Now()
				r := analyze(f)
				analyzed := time.Now()
				if len(r.Errors()) != 0 {
					b.Fatal(r.Errors())
				}
				g, err := compileLLVMModule(r, OptimizationLevel0, DefaultPackedLoweringProfile())
				lowered := time.Now()
				if err != nil {
					b.Fatal(err)
				}
				err = g.writeObjectFile(out, OptimizationLevel0)
				g.dispose()
				emitted := time.Now()
				if err != nil {
					b.Fatal(err)
				}
				parsing += parsed.Sub(start)
				analysis += analyzed.Sub(parsed)
				lowering += lowered.Sub(analyzed)
				emission += emitted.Sub(lowered)
				after := session.Stats()
				reused += after.PrimaryBodiesReused - before.PrimaryBodiesReused
				checked += after.PrimaryBodiesChecked - before.PrimaryBodiesChecked
			}
			b.StopTimer()
			for key, elapsed := range map[string]time.Duration{"parse-ms/edit": parsing, "analysis-ms/edit": analysis, "lower-ms/edit": lowering, "object-ms/edit": emission} {
				b.ReportMetric(float64(elapsed)/float64(time.Millisecond)/float64(b.N), key)
			}
			if retained {
				b.ReportMetric(float64(reused)/float64(b.N), "body-reuses/edit")
				b.ReportMetric(float64(checked)/float64(b.N), "body-checks/edit")
			}
		})
	}
}
