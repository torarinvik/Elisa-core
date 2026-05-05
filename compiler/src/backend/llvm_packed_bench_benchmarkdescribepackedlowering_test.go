//go:build cgo

package backend

import "testing"

func BenchmarkDescribePackedLowering(b *testing.B) {
	result := parseAndAnalyzeBackendBenchmarkSource(b, "packed_describe_bench.elisa", packedLoweringRetainedReadsBenchmarkSource)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DescribePackedLowering(result, DefaultPackedLoweringProfile()); err != nil {
			b.Fatalf("DescribePackedLowering returned error: %v", err)
		}
	}
}
