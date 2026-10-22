package semantic

import "testing"

func TestResolvedStructFieldsMemoReusesFrozenFieldList(t *testing.T) {
	a := &Analyzer{typeShapesFrozen: true}
	tuple := &TupleType{Fields: []TupleField{{Name: "value", Type: &BuiltinType{Name: "i64"}}}}
	first, ok := a.resolvedStructFields(tuple)
	if !ok || len(first) != 1 || first[0].Name != "value" {
		t.Fatalf("unexpected resolved fields: ok=%t fields=%#v", ok, first)
	}
	second, ok := a.resolvedStructFields(tuple)
	if !ok || len(second) != 1 || &first[0] != &second[0] {
		t.Fatalf("expected cached immutable field list, got ok=%t fields=%#v", ok, second)
	}
}
