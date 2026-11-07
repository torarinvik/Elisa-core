package semantic

import "testing"

var storeFlowVariableNameSink bool

func BenchmarkStoreFlowVariableName(b *testing.B) {
	for _, name := range []string{"source", "a_longer_semantic_binding_name", "Äther", "mixed.qualified"} {
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				storeFlowVariableNameSink = storeFlowVariableName(name)
			}
		})
	}
}
