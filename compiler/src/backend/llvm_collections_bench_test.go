//go:build cgo

package backend

import "testing"

func BenchmarkGenerateLLVMIRInlineVecFixture(b *testing.B) {
	src := loadPackedBenchSourceFromFile(b, "Code/test_programs/inline_vec.elisa")
	result := parseAndAnalyzeBackendBenchmarkSource(b, "inline_vec_fixture_bench.elisa", src)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel0, DefaultPackedLoweringProfile()); err != nil {
			b.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
		}
	}
}

func BenchmarkGenerateLLVMIRBuilderFixture(b *testing.B) {
	src := loadPackedBenchSourceFromFile(b, "Code/test_programs/array_builders.elisa")
	result := parseAndAnalyzeBackendBenchmarkSource(b, "array_builder_fixture_bench.elisa", src)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel0, DefaultPackedLoweringProfile()); err != nil {
			b.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
		}
	}
}
