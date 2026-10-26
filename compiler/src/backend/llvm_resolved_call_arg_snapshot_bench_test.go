//go:build cgo

package backend

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

const resolvedCallArgSnapshotBenchmarkCalls = 1024

func resolvedCallArgSnapshotBenchmarkSource(calls int) string {
	var source strings.Builder
	fmt.Fprint(&source, `@callconv(c)
extern checked(value: i64) -> i64 ensure result == value

def leaf(value: i64) -> i64:
    return value
`)
	for i := 0; i < calls; i++ {
		fmt.Fprintf(&source, "def ordinary_%d(value: i64) -> i64:\n    return leaf(value)\n\n", i)
	}
	fmt.Fprint(&source, `def checked_call(value: i64) -> i64:
    return checked(value)

def main() -> i32:
    return 0
`)
	return source.String()
}

// This lowers a real O0 LLVM module with many ordinary call sites and one
// checked extern boundary. The source and semantic analysis stay outside the
// timer; the benchmark measures full backend module generation.
func BenchmarkGenerateLLVMIRResolvedCallsWithExternEnsure(b *testing.B) {
	result := parseAndAnalyzeBackendBenchmarkSource(b, "resolved_call_arg_snapshot_bench.elisa", resolvedCallArgSnapshotBenchmarkSource(resolvedCallArgSnapshotBenchmarkCalls))
	checkIR, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		b.Fatalf("preflight LLVM generation: %v", err)
	}
	if !strings.Contains(checkIR, "extern postcondition failed") || !strings.Contains(checkIR, "icmp eq i64") {
		b.Fatalf("benchmark fixture did not lower its parameter-dependent extern ensure:\n%s", checkIR)
	}
	if calls := strings.Count(checkIR, "call i64 @leaf("); calls != resolvedCallArgSnapshotBenchmarkCalls {
		b.Fatalf("fixture lowered %d ordinary calls, want %d", calls, resolvedCallArgSnapshotBenchmarkCalls)
	}
	b.Logf("exact LLVM IR SHA256=%x", sha256.Sum256([]byte(checkIR)))
	b.ReportAllocs()
	b.ReportMetric(resolvedCallArgSnapshotBenchmarkCalls+1, "calls/op")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0); err != nil {
			b.Fatalf("LLVM generation: %v", err)
		}
	}
}
