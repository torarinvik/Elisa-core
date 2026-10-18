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

var benchmarkLocalPlanSink []*primitiveLocalInitializer

// BenchmarkPrimitiveLocalInitializerPlanning isolates the per-function plan
// construction from parser and analyzer costs. It makes regressions in plan
// complexity visible as the number of independent locals grows.
func BenchmarkPrimitiveLocalInitializerPlanning(b *testing.B) {
	const locals = 128
	var source strings.Builder
	fmt.Fprintln(&source, "def work(value: i64) -> i64:")
	for i := 0; i < locals; i++ {
		fmt.Fprintf(&source, "    local%d: i64 = value + 1\n", i)
	}
	fmt.Fprintln(&source, "    return value + 1")
	file := func() *ast.File {
		l := lexer.New("incremental_plan_benchmark.elisa", []byte(source.String()))
		p := parser.New(l.Tokenize())
		parsed := p.ParseFile("incremental_plan_benchmark.elisa")
		if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("invalid local-plan benchmark source")
		}
		return parsed
	}()
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		b.Fatal("local-plan benchmark function missing")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inputs, valid := primitiveLocalInitializers(fn)
		if !valid || len(inputs) != locals {
			b.Fatalf("local-plan benchmark declined fixture: valid=%t inputs=%d", valid, len(inputs))
		}
		benchmarkLocalPlanSink = inputs
	}
}

// BenchmarkIncrementalLocalInitializerEdit compares cold analysis with one
// local-initializer edit against a retained base revision. Parsing is excluded
// from both paths; retained timing includes cache validation and publication.
// This synthetic fixture measures edit locality in the admitted grammar, not
// whole-project or edit-to-executable latency.
func BenchmarkIncrementalLocalInitializerEdit(b *testing.B) {
	const functionCount, localsPerFunction = 8, 16
	var base strings.Builder
	var edited strings.Builder
	for fn := 0; fn < functionCount; fn++ {
		fmt.Fprintf(&base, "def work%d(value: i64) -> i64:\n", fn)
		fmt.Fprintf(&edited, "def work%d(value: i64) -> i64:\n", fn)
		for local := 0; local < localsPerFunction; local++ {
			name := fmt.Sprintf("local%d", local)
			delta := "1"
			if fn == 0 && local == localsPerFunction-1 {
				fmt.Fprintf(&edited, "    %s: i64 = value + 2\n", name)
			} else {
				fmt.Fprintf(&edited, "    %s: i64 = value + %s\n", name, delta)
			}
			fmt.Fprintf(&base, "    %s: i64 = value + %s\n", name, delta)
		}
		fmt.Fprintf(&base, "    return value + 1\n\n")
		fmt.Fprintf(&edited, "    return value + 1\n\n")
	}
	baseSource, editedSource := base.String(), edited.String()
	parse := func(source string) *ast.File {
		l := lexer.New("incremental_edit_benchmark.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		file := p.ParseFile("incremental_edit_benchmark.elisa")
		if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
			b.Fatal("invalid incremental edit benchmark source")
		}
		return file
	}
	options := AnalyzeOptions{}
	coldExpected := AnalyzeWithOptions(parse(editedSource), options)
	if len(coldExpected.Errors()) != 0 {
		b.Fatal(coldExpected.Errors())
	}

	b.Run("cold-edit", func(b *testing.B) {
		assertIncrementalResultParity(b, coldExpected, AnalyzeWithOptions(parse(editedSource), options))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			file := parse(editedSource)
			b.StartTimer()
			result := AnalyzeWithOptions(file, options)
			b.StopTimer()
			if len(result.Errors()) != len(coldExpected.Errors()) {
				b.Fatalf("diagnostics changed: cold=%v result=%v", coldExpected.Errors(), result.Errors())
			}
		}
	})

	b.Run("session-first-analysis", func(b *testing.B) {
		// This path exercises initializer plans whose product keys are not yet
		// present. It measures the known-miss shortcut separately from retained
		// edits, where every initializer already has a product to validate.
		warmSession := NewIncrementalAnalysisSession()
		warmResult := warmSession.Analyze(parse(editedSource), options)
		if len(warmResult.Errors()) != len(coldExpected.Errors()) {
			b.Fatalf("first session analysis changed diagnostics: cold=%v session=%v", coldExpected.Errors(), warmResult.Errors())
		}
		assertIncrementalResultParity(b, coldExpected, warmResult)
		b.ReportAllocs()
		b.ResetTimer()
		var queries, checked, reused uint64
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			session := NewIncrementalAnalysisSession()
			file := parse(editedSource)
			b.StartTimer()
			result := session.Analyze(file, options)
			b.StopTimer()
			if len(result.Errors()) != len(coldExpected.Errors()) {
				b.Fatalf("first session analysis changed diagnostics: cold=%v session=%v", coldExpected.Errors(), result.Errors())
			}
			stats := session.Stats()
			queries += stats.Queries.Executed
			checked += stats.LocalInitializersChecked
			reused += stats.LocalInitializersReused
		}
		b.ReportMetric(float64(queries)/float64(b.N), "queries/initial-analysis")
		b.ReportMetric(float64(checked)/float64(b.N), "local-init-checks/initial")
		b.ReportMetric(float64(reused)/float64(b.N), "local-init-reuses/initial")
	})

	b.Run("retained-edit", func(b *testing.B) {
		session := NewIncrementalAnalysisSession()
		if result := session.Analyze(parse(baseSource), options); len(result.Errors()) != 0 {
			b.Fatal(result.Errors())
		}
		warmEdit := session.Analyze(parse(editedSource), options)
		if len(warmEdit.Errors()) != len(coldExpected.Errors()) {
			b.Fatalf("edited result differs in diagnostic count: cold=%v retained=%v", coldExpected.Errors(), warmEdit.Errors())
		}
		assertIncrementalResultParity(b, coldExpected, warmEdit)
		// Return to the base revision so each timed operation measures a single
		// edit from the same starting source, with a fresh AST for each analysis.
		if result := session.Analyze(parse(baseSource), options); len(result.Errors()) != 0 {
			b.Fatal(result.Errors())
		}
		b.ReportAllocs()
		b.ResetTimer()
		var bodies, reused, checked, nodes uint64
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			baseFile := parse(baseSource)
			editedFile := parse(editedSource)
			if result := session.Analyze(baseFile, options); len(result.Errors()) != 0 {
				b.Fatal(result.Errors())
			}
			before := session.Stats()
			b.StartTimer()
			result := session.Analyze(editedFile, options)
			b.StopTimer()
			if len(result.Errors()) != len(coldExpected.Errors()) {
				b.Fatalf("diagnostics changed: cold=%v retained=%v", coldExpected.Errors(), result.Errors())
			}
			after := session.Stats()
			bodies += after.PrimaryBodiesReused - before.PrimaryBodiesReused
			reused += after.LocalInitializersReused - before.LocalInitializersReused
			checked += after.LocalInitializersChecked - before.LocalInitializersChecked
			nodes += after.ReusedExpressionNodes - before.ReusedExpressionNodes
		}
		b.ReportMetric(float64(bodies)/float64(b.N), "body-reuses/edit")
		b.ReportMetric(float64(reused)/float64(b.N), "local-init-reuses/edit")
		b.ReportMetric(float64(checked)/float64(b.N), "local-init-checks/edit")
		b.ReportMetric(float64(nodes)/float64(b.N), "expr-nodes-reused/edit")
	})
}
