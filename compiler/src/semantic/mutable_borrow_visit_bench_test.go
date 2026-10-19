package semantic

import (
	"fmt"
	"testing"
)

func BenchmarkMutableBorrowProductionVisits(b *testing.B) {
	leaf := &BuiltinType{Name: "i64"}
	for _, width := range []int{1, 7, 8, 9, 32, 64} {
		b.Run(fmt.Sprint(width), func(b *testing.B) {
			root := &StructType{Name: "R", Fields: map[string]Field{}}
			for i := 0; i < width; i++ {
				root.Fields[fmt.Sprint(i)] = Field{Type: &OptionalType{Value: leaf}}
			}
			a := &Analyzer{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if a.hasMutableBorrowCapability(root) {
					b.Fatal("unexpected mutable capability")
				}
			}
		})
	}
}
