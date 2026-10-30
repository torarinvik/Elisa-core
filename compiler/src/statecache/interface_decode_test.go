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

type interfaceMemoBoth struct{ Value int }

func (n *interfaceMemoBoth) Number() int   { return n.Value }
func (n *interfaceMemoBoth) Label() string { return "label" }

func TestInterfaceMemoSeparatesDestinationInterfaces(t *testing.T) {
	type numbered interface{ Number() int }
	type labelled interface{ Label() string }
	type row struct {
		A numbered
		B labelled
		C any
	}
	node := &interfaceMemoBoth{Value: 42}
	source := []row{{node, node, node}, {node, node, node}}
	r := New([]reflect.Type{reflect.TypeFor[[]row](), reflect.TypeFor[interfaceMemoBoth]()}, nil, nil)
	data, err := r.Encode(source)
	if err != nil {
		t.Fatal(err)
	}
	value, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range value.([]row) {
		if got.A.Number() != 42 || got.B.Label() != "label" || got.C.(*interfaceMemoBoth) != got.A.(*interfaceMemoBoth) || got.B.(*interfaceMemoBoth) != got.A.(*interfaceMemoBoth) {
			t.Fatal("destination interface methods or alias changed")
		}
	}
}

func BenchmarkStateMixedInterfacePointers(b *testing.B) {
	r := New([]reflect.Type{reflect.TypeFor[[]any](), reflect.TypeFor[graph](), reflect.TypeFor[interfaceMemoBoth]()}, nil, nil)
	nodes := []any{&graph{Name: "shared"}, &interfaceMemoBoth{Value: 42}}
	values := make([]any, 10000)
	for i := range values {
		values[i] = nodes[i%len(nodes)]
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

func TestInterfaceMemoCollisionsPreserveConcreteTypes(t *testing.T) {
	nodes := []any{new([1]int), new([2]int), new([3]int), new([4]int), new([5]int), new([6]int), new([7]int), new([8]int), new([9]int)}
	types := []reflect.Type{reflect.TypeFor[[]any]()}
	for i, node := range nodes {
		reflect.ValueOf(node).Elem().Index(0).SetInt(int64(i + 1))
		types = append(types, reflect.TypeOf(node))
	}
	var source []any
	for i := 0; i < 5; i++ {
		source = append(source, nodes...)
	}
	r := New(types, nil, nil)
	data, err := r.Encode(source)
	if err != nil {
		t.Fatal(err)
	}
	value, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := value.([]any)
	if !reflect.DeepEqual(source, got) {
		t.Fatal("cache collision changed a concrete interface value")
	}
	for i := range got {
		if reflect.ValueOf(got[i]).Pointer() != reflect.ValueOf(got[i%len(nodes)]).Pointer() {
			t.Fatal("cache collision lost a pointer alias")
		}
	}
}
