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
}
