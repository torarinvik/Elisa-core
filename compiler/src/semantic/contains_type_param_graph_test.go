package semantic

import "testing"

func TestContainsTypeParamSharedAndRecursiveGraphs(t *testing.T) {
	leaf := &TupleType{Fields: []TupleField{{Type: &BuiltinType{Name: "i64"}}}}
	var graph Type = leaf
	for i := 0; i < 40; i++ {
		graph = &TupleType{Fields: []TupleField{{Type: graph}, {Type: graph}}}
	}
	if containsTypeParam(graph) {
		t.Fatal("concrete shared graph contains a type parameter")
	}
	leaf.Fields[0].Type = &TypeParamType{Name: "T"}
	if !containsTypeParam(graph) {
		t.Fatal("shared graph lost its type parameter")
	}
	recursive := &EnumType{Name: "Recursive"}
	cycle := &TupleType{}
	recursive.Variants = []*EnumVariant{{Payload: []Type{cycle, &TypeParamType{Name: "T"}}}}
	cycle.Fields = []TupleField{{Type: &RefType{Elem: recursive}}}
	if !containsTypeParam(recursive) {
		t.Fatal("recursive enum lost its type parameter")
	}
	recursive.Variants[0].Payload = []Type{cycle}
	if containsTypeParam(recursive) {
		t.Fatal("concrete recursive enum contains a type parameter")
	}

	var enumGraph Type = &TypeParamType{Name: "U"}
	for i := 0; i < 40; i++ {
		enumGraph = &EnumType{Name: "Nested", Variants: []*EnumVariant{{Payload: []Type{enumGraph, enumGraph}}}}
	}
	if !containsTypeParam(enumGraph) {
		t.Fatal("deep shared enum graph lost its type parameter")
	}
	var concrete Type = &BuiltinType{Name: "i64"}
	for i := 0; i < 40; i++ {
		concrete = &EnumType{Name: "Nested", Variants: []*EnumVariant{{Payload: []Type{concrete, concrete}}}}
	}
	if containsTypeParam(concrete) {
		t.Fatal("concrete deep enum graph contains a type parameter")
	}
}

func TestContainsTypeParamSharedAndRecursiveGenericGraphs(t *testing.T) {
	base := &GenericInstanceType{Name: "Leaf", Args: []Type{&BuiltinType{Name: "i64"}}}
	var graph Type = base
	for i := 0; i < 40; i++ {
		graph = &GenericInstanceType{Name: "Pair", Args: []Type{graph, graph}}
	}
	if containsTypeParam(graph) {
		t.Fatal("concrete generic graph contains a type parameter")
	}
	base.Args[0] = &TypeParamType{Name: "T"}
	if !containsTypeParam(graph) {
		t.Fatal("shared generic graph lost its type parameter")
	}
	cycle := &GenericInstanceType{Name: "Cycle"}
	cycle.Args = []Type{cycle, &TypeParamType{Name: "U"}}
	if !containsTypeParam(cycle) {
		t.Fatal("recursive generic graph lost its type parameter")
	}
	cycle.Args = []Type{cycle}
	if containsTypeParam(cycle) {
		t.Fatal("concrete recursive generic graph contains a type parameter")
	}
}
