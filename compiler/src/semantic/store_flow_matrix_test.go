package semantic

import "testing"

var storeFlowMatrixSink *storeFlowSummary

func TestStoreFlowSummaryRowsIndependent(t *testing.T) {
	for _, n := range []int{0, 1, 8, 32} {
		s := newStoreFlowSummary(n)
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if s.conn[i][j] != (i == j) || s.reach[i][j] != (i == j) {
					t.Fatalf("arity %d: initial edge %d,%d", n, i, j)
				}
			}
		}
		if n > 1 {
			s.conn[0][1] = true
			if s.conn[1][0] || s.reach[0][1] {
				t.Fatal("matrix rows or matrices alias")
			}
			s.conn[1][0] = true
			grown := append(s.conn[0], true)
			grown[n] = false
			if !s.conn[1][0] || !s.conn[1][1] {
				t.Fatal("append overwrote next row")
			}
		}
	}
}

func BenchmarkStoreFlowSummaryAllocation(b *testing.B) {
	for _, tc := range []struct {
		name string
		n    int
	}{{"arity8", 8}, {"arity32", 32}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				storeFlowMatrixSink = newStoreFlowSummary(tc.n)
			}
		})
	}
}
