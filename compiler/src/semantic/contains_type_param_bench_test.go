package semantic

import "testing"

func BenchmarkContainsTypeParam(b *testing.B) {
	leaf := &EnumType{Name: "Leaf"}
	var graph Type = leaf
	for i := 0; i < 14; i++ {
		graph = &TupleType{Fields: []TupleField{{Type: graph}, {Type: graph}}}
	}
	for _, tc := range []struct {
		name  string
		value Type
	}{
		{"scalar", &BuiltinType{Name: "i64"}},
		{"shallow", &RefType{Elem: &BuiltinType{Name: "i64"}}},
		{"shared_graph", graph},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if containsTypeParam(tc.value) {
					b.Fatal("unexpected type parameter")
				}
			}
		})
	}
}

func BenchmarkContainsTypeParamSharedEnumGraph(b *testing.B) {
	var graph Type = &BuiltinType{Name: "i64"}
	for i := 0; i < 14; i++ {
		graph = &EnumType{Name: "Level", Variants: []*EnumVariant{{Payload: []Type{graph, graph}}}}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if containsTypeParam(graph) {
			b.Fatal("unexpected type parameter")
		}
	}
}

func BenchmarkContainsTypeParamSharedGenericGraph(b *testing.B) {
	var graph Type = &BuiltinType{Name: "i64"}
	for i := 0; i < 14; i++ {
		graph = &GenericInstanceType{Name: "Pair", Args: []Type{graph, graph}}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if containsTypeParam(graph) {
			b.Fatal("unexpected type parameter")
		}
	}
}
