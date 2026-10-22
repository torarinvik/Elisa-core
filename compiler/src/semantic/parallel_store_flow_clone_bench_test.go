package semantic

import (
	"strconv"
	"testing"

	"elisacore/src/ast"
)

// The settled matrices grow quadratically with parameter count. Forking a body
// worker should retain them while giving its later edge-kind solve private state.
func BenchmarkStoreFlowSummaryWorkerClone(b *testing.B) {
	for _, arity := range []int{4, 32, 128} {
		b.Run(strconv.Itoa(arity), func(b *testing.B) {
			source := make(map[*ast.FuncDecl]*storeFlowSummary, 128)
			for i := 0; i < 128; i++ {
				source[&ast.FuncDecl{}] = newStoreFlowSummary(arity)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if len(cloneStoreFlowSummariesForBodyWorker(source)) != 128 {
					b.Fatal("lost a settled summary")
				}
			}
		})
	}
}
