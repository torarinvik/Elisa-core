//go:build cgo

package backend

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Parsing/semantic checking is excluded. Novel edits cannot hit a complete
// prior revision. The unchanged case measures the cost of an existing hit.
func BenchmarkIncrementalConstantLocality(b *testing.B) {
	const base = `extern atoi(value: cstr) -> int

def left(value: int) -> int:
    trusted Unsafe.RawExtern:
        return atoi("00000041") + value

def right(value: int) -> int:
    return value + 1

def main() -> int:
    return left(0) + right(0)
`
	for _, edit := range []bool{false, true} {
		name := "unchanged"
		if edit {
			name = "novel-edit"
		}
		b.Run(name, func(b *testing.B) {
			result := parseAndAnalyzeBackendBenchmarkSource(b, "constant_locality_bench.elisa", base)
			session := NewIncrementalObjectSession(2, 1<<24)
			session.minWeight = 0
			object := filepath.Join(b.TempDir(), "main.o")
			options := LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}
			if err := session.WriteObject(result, object, options); err != nil {
				b.Fatal(err)
			}
			before := session.Stats()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if edit {
					b.StopTimer()
					src := strings.Replace(base, "\"00000041\"", fmt.Sprintf("\"%08d\"", i+42), 1)
					result = parseAndAnalyzeBackendBenchmarkSource(b, "constant_locality_bench.elisa", src)
					b.StartTimer()
				}
				if err := session.WriteObject(result, object, options); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			after := session.Stats()
			emitted := after.PartitionsEmitted - before.PartitionsEmitted
			reused := after.PartitionsReused - before.PartitionsReused
			if emitted+reused != uint64(2*b.N) || after.SerialFallbacks != 0 {
				b.Fatal("invalid shard workload", after)
			}
			if !edit && emitted != 0 {
				b.Fatal("unchanged input missed", after)
			}
			if edit && emitted == 0 {
				b.Fatal("novel edits did not invalidate", after)
			}
			b.ReportMetric(float64(emitted)/float64(b.N), "emitted-parts/edit")
			b.ReportMetric(float64(reused)/float64(b.N), "reused-parts/edit")
		})
	}
}
