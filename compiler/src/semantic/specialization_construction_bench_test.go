//go:build cgo

package semantic

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// Keep only one batch alive: construction allocation includes temporary Analyzer
// copies but this benchmark does not accidentally retain every historical batch.
var specializationBenchmarkProducts []*SpecializedFunction

func specializationBenchmarkSource(unrelated int) string {
	var source strings.Builder
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&source, "struct Concrete%d:\n    value: i64\n\n", i)
	}
	source.WriteString("def bounce[T](value: T, depth: i64 = 0) -> T:\n    return value\n\ndef first[T](value: T) -> T:\n    return bounce(value)\n\n")
	for i := 0; i < unrelated; i++ {
		fmt.Fprintf(&source, "def unrelated_%d(value: i64) -> i64:\n    const delta: i64 = %d\n    return value + delta\n\n", i, i)
	}
	return source.String()
}

func specializationBenchmarkAnalyze(b *testing.B, source string) (*Result, *ast.FuncDecl, []Type) {
	b.Helper()
	lex := lexer.New("specialization-benchmark.elisa", []byte(source))
	tokens := lex.Tokenize()
	if errors := lex.Errors(); len(errors) != 0 {
		b.Fatal(errors)
	}
	parse := parser.New(tokens)
	file := parse.ParseFile("specialization-benchmark.elisa")
	if errors := parse.Errors(); len(errors) != 0 {
		b.Fatal(errors)
	}
	root := Analyze(file)
	if errors := root.Errors(); len(errors) != 0 {
		b.Fatal(errors)
	}
	var target *ast.FuncDecl
	for _, decl := range root.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "first" {
			target = fn
			break
		}
	}
	if target == nil {
		b.Fatal("missing first generic")
	}
	arguments := make([]Type, 8)
	for i := range arguments {
		arguments[i] = root.NamedTypes[fmt.Sprintf("Concrete%d", i)]
		if arguments[i] == nil {
			b.Fatal("missing concrete type")
		}
	}
	return root, target, arguments
}

// Setup is intentionally a separate benchmark. Construction below excludes
// source creation, lex/parse/analyze, shared canonical identity indexing, and
// fixture validation from both elapsed time and allocation counts.
func BenchmarkSpecializationAnalyzeSetup(b *testing.B) {
	for _, size := range []int{0, 128, 512} {
		b.Run(fmt.Sprintf("unrelated_%d", size), func(b *testing.B) {
			source := specializationBenchmarkSource(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				root, _, _ := specializationBenchmarkAnalyze(b, source)
				runtime.KeepAlive(root)
			}
		})
	}
}

func BenchmarkSpecializationConstruction(b *testing.B) {
	for _, size := range []int{0, 128, 512} {
		b.Run(fmt.Sprintf("unrelated_%d", size), func(b *testing.B) {
			root, target, arguments := specializationBenchmarkAnalyze(b, specializationBenchmarkSource(size))
			root.BeginSharedCodegen()
			defer root.EndSharedCodegen()
			// Establish shared canonical indexes once, as production does. This also
			// validates actual private call/default facts before measurement.
			warmed, err := root.SpecializeFunction(target, []Type{arguments[0]})
			if err != nil {
				b.Fatal(err)
			}
			call := warmed.Decl.Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
			if len(call.ResolvedArgs) != 2 || warmed.Result.ValueExprTypes(call) != arguments[0] {
				b.Fatal("invalid benchmark specialization")
			}
			products := make([]*SpecializedFunction, len(arguments))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j, argument := range arguments {
					// Same production constructor, deliberately bypassing the memo lookup so
					// every observation measures a new full construction, never an accidental hit.
					product, err := root.specializePrivateFunction(target, []Type{argument})
					if err != nil {
						b.Fatal(err)
					}
					products[j] = product
				}
				specializationBenchmarkProducts = products
			}
			b.StopTimer()
			b.ReportMetric(float64(len(arguments)), "products/op")
			runtime.KeepAlive(root)
			specializationBenchmarkProducts = nil
		})
	}
}

func BenchmarkSpecializationMemoHit(b *testing.B) {
	for _, size := range []int{0, 128, 512} {
		b.Run(fmt.Sprintf("unrelated_%d", size), func(b *testing.B) {
			root, target, arguments := specializationBenchmarkAnalyze(b, specializationBenchmarkSource(size))
			root.BeginSharedCodegen()
			defer root.EndSharedCodegen()
			products := make([]*SpecializedFunction, len(arguments))
			bindings := make([][]Type, len(arguments))
			for i, argument := range arguments {
				bindings[i] = []Type{argument}
				product, err := root.SpecializeFunction(target, bindings[i])
				if err != nil {
					b.Fatal(err)
				}
				products[i] = product
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				index := i % len(arguments)
				product, err := root.SpecializeFunction(target, bindings[index])
				if err != nil || product != products[index] {
					b.Fatal("memo miss", err)
				}
				runtime.KeepAlive(product)
			}
			b.StopTimer()
			runtime.KeepAlive(root)
		})
	}
}
