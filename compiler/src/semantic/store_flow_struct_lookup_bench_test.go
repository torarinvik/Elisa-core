package semantic

import (
	"fmt"
	"testing"
)

func BenchmarkStoreFlowStructNamed(b *testing.B) {
	a := &Analyzer{namedTypes: make(map[string]Type), storeFlowStructLookup: make(map[string]Type)}
	for i := 0; i < 1024; i++ {
		name := fmt.Sprintf("Module%d.Struct%d", i, i)
		a.namedTypes[name] = &StructType{Name: name}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if a.storeFlowStructNamed("Struct1000") == nil {
			b.Fatal("missing type")
		}
	}
}
