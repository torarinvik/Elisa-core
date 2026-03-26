//go:build cgo

package backend

import (
	"strings"
	"testing"

	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/semantic"
)

const packedLoweringBenchmarkSource = `@packed_profile(retained_reads)
packed enum Expr:
    common:
        span: i64
        depth: i32
    Lit(value: i64)
    Add(left: Expr, right: Expr)

def checksum(frozen: Expr.Store[Frozen]) -> i64:
    total: mutable i64 = 0
    i: mutable usize = 0u
    while i < frozen.count:
        node: Expr = frozen[i]
        total <- total + node.span
        total <- total + node.depth.i64()
        i <- i + 1u
    return total
`

func parseAndAnalyzeBackendBenchmarkSource(b *testing.B, filename string, src string) *semantic.Result {
	b.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		b.Fatalf("lexer errors:\n%s", strings.Join(errs, "\n"))
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		b.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	result := semantic.Analyze(file)
	if errs := result.Errors(); len(errs) > 0 {
		b.Fatalf("semantic errors:\n%s", strings.Join(errs, "\n"))
	}
	return result
}

func benchmarkPackedLowering(b *testing.B, profile PackedLoweringProfile) {
	b.Helper()
	result := parseAndAnalyzeBackendBenchmarkSource(b, "packed_lowering_bench.llcontext", packedLoweringBenchmarkSource)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel0, profile); err != nil {
			b.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
		}
	}
}

func BenchmarkGenerateLLVMIRPackedLoweringCanonical(b *testing.B) {
	benchmarkPackedLowering(b, DefaultPackedLoweringProfile())
}

func BenchmarkGenerateLLVMIRPackedLoweringWordHandle(b *testing.B) {
	profile, err := LegacyPackedLoweringProfile(PackedEnumABIWordHandle)
	if err != nil {
		b.Fatalf("LegacyPackedLoweringProfile returned error: %v", err)
	}
	benchmarkPackedLowering(b, profile)
}

func BenchmarkDescribePackedLowering(b *testing.B) {
	result := parseAndAnalyzeBackendBenchmarkSource(b, "packed_describe_bench.llcontext", packedLoweringBenchmarkSource)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DescribePackedLowering(result, DefaultPackedLoweringProfile()); err != nil {
			b.Fatalf("DescribePackedLowering returned error: %v", err)
		}
	}
}
