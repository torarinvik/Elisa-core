package statecache

import (
	"reflect"
	"testing"
)

func TestInterfaceReferencesPreserveTypedNilsAndAliases(t *testing.T) {
	type root struct {
		Values []any
		Node   *graph
		Map    map[string]int
		Slice  []int
	}
	node := &graph{Name: "shared"}
	m := map[string]int{"a": 1}
	s := []int{2, 3}
	original := &root{Values: []any{node, m, s, (*graph)(nil), map[string]int(nil), []int(nil), nil}, Node: node, Map: m, Slice: s}
	r := New([]reflect.Type{reflect.TypeFor[root](), reflect.TypeFor[graph]()}, nil, nil)
	data, err := r.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.(*root)
	if !reflect.DeepEqual(original, got) {
		t.Fatal("interface reference value changed")
	}
	if got.Values[0].(*graph) != got.Node {
		t.Fatal("interface pointer identity lost")
	}
	got.Values[1].(map[string]int)["b"] = 4
	if got.Map["b"] != 4 {
		t.Fatal("interface map identity lost")
	}
	got.Values[2].([]int)[0] = 5
	if got.Slice[0] != 5 {
		t.Fatal("interface slice identity lost")
	}
	for _, i := range []int{3, 4, 5} {
		if got.Values[i] == nil || !reflect.ValueOf(got.Values[i]).IsNil() {
			t.Fatal("typed nil became nil interface")
		}
	}
	if got.Values[6] != nil {
		t.Fatal("nil interface acquired a dynamic type")
	}
}

func BenchmarkStateInterfacePointers(b *testing.B) {
	r := New([]reflect.Type{reflect.TypeFor[[]any](), reflect.TypeFor[graph]()}, nil, nil)
	node := &graph{Name: "shared"}
	values := make([]any, 10000)
	for i := range values {
		values[i] = node
	}
	data, err := r.Encode(values)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Decode(data); err != nil {
			b.Fatal(err)
		}
	}
}
