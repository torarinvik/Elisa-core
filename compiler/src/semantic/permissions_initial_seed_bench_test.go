//go:build cgo

package semantic

import (
	"fmt"
	"testing"
)

func BenchmarkPermissionInitialSparse(b *testing.B) {
	for _, n := range []int{256, 2048} {
		b.Run(fmt.Sprintf("functions%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				f := newPermissionGraphFixture(n)
				f.edge(0, 1)
				f.edge(1, -1)
				b.StartTimer()
				count := f.a.inferFunctionPermissionEffectsWorklist(f.decls)
				b.ReportMetric(float64(count), "evaluations/op")
			}
		})
	}
}
