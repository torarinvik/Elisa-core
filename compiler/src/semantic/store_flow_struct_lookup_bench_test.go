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

func BenchmarkStoreFlowStructManyNames(b *testing.B) {
	a := &Analyzer{namedTypes: make(map[string]Type)}
	names := make([]string, 64)
	for i := 0; i < 1024; i++ {
		name := fmt.Sprintf("Module%d.Struct%d", i, i)
		a.namedTypes[name] = &StructType{Name: name}
		if i < len(names) {
			names[i] = fmt.Sprintf("Struct%d", i)
		}
	}
	a.storeFlowNamedTypes = make(map[string]string, len(names))
	for _, name := range names {
		a.storeFlowNamedTypes[name] = name
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.storeFlowStructLookup = make(map[string]Type)
		a.storeFlowStructLookupComplete = false
		for _, name := range names {
			if a.storeFlowStructNamed(name) == nil {
				b.Fatal("missing type")
			}
		}
	}
}

func BenchmarkStoreFlowStructRepeatedSummaries(b *testing.B) {
	named, locals := make(map[string]Type), make(map[string]string)
	names := make([]string, 64)
	for i := 0; i < 1024; i++ {
		name := fmt.Sprintf("Struct%d", i)
		named["Module."+name] = &StructType{Name: name}
		if i < len(names) {
			names[i], locals[name] = name, name
		}
	}
	for _, frozen := range []bool{false, true} {
		label := "per-summary"
		if frozen {
			label = "worker-index"
		}
		b.Run(label, func(b *testing.B) {
			a := &Analyzer{namedTypes: named, storeFlowNamedTypes: locals, typeShapesFrozen: frozen}
			if frozen {
				a.parallel = &parallelWorkerState{}
				a.storeFlowStructNamed(names[0])
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				a.storeFlowStructLookup = make(map[string]Type)
				a.storeFlowStructLookupComplete = false
				for _, name := range names {
					if a.storeFlowStructNamed(name) == nil {
						b.Fatal("missing type")
					}
				}
			}
		})
	}
}
