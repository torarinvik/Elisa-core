package statecache

import (
	"reflect"
	"runtime"
	"testing"
)

// Framed variants of the existing identity, hidden-capacity and map fixtures.
func framedGraphRegistry() *Registry {
	return newFramedTestRegistry([]reflect.Type{reflect.TypeFor[graph]()}, nil, nil)
}
func newFramedTestRegistry(types []reflect.Type, p FieldPolicy, a FieldAccess) *Registry {
	r := New(types, p, a)
	r.EnableParallelDecode()
	return r
}
func TestFramedGraphRoundTripIdentity(t *testing.T) {
	root := &graph{Name: "root", Data: []byte{0, 0xff, 3}, Empty: []byte{}, Signed: -9223372036854775807, Float: -2.75}
	child := &graph{Name: "child", Next: root}
	root.Next = child
	root.Alias = child
	root.Dynamic = child
	root.Values = map[string]*graph{"root": root, "child": child}
	root.Same = root.Values
	root.Copy = root.Data
	data, err := framedGraphRegistry().EncodeFramed(root)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := framedGraphRegistry().Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := restored.(*graph)
	if got.Next.Next != got || got.Next != got.Alias || got.Dynamic.(*graph) != got.Next || got.Values["child"] != got.Next {
		t.Fatal("pointer identity lost")
	}
	got.Values["other"] = got
	if got.Same["other"] != got {
		t.Fatal("map identity lost")
	}
	got.Data[0] = 8
	if got.Copy[0] != 8 {
		t.Fatal("slice identity lost")
	}
	if got.Empty == nil || got.Nil != nil || got.Signed != root.Signed || got.Float != root.Float {
		t.Fatal("scalar or nil state changed")
	}
	if !reflect.DeepEqual(root.Data, []byte{0, 0xff, 3}) {
		t.Fatal("decode aliases original input graph")
	}
}
func TestFramedGraphPreservesInteriorAliases(t *testing.T) {
	type interior struct {
		Values  []int
		Tail    []int
		Element *int
	}
	r := newFramedTestRegistry([]reflect.Type{reflect.TypeFor[interior]()}, nil, nil)
	values := []int{1, 2, 3}
	data, err := r.EncodeFramed(&interior{Values: values, Tail: values[1:], Element: &values[1]})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.(*interior)
	*got.Element = 42
	if got.Values[1] != 42 || got.Tail[0] != 42 || got.Element != &got.Values[1] {
		t.Fatal("interior aliases were duplicated")
	}
	if cap(got.Values) != cap(values) || cap(got.Tail) != cap(values[1:]) {
		t.Fatal("slice capacities changed")
	}
}
func TestFramedGraphPreservesStructAndArrayFieldAliases(t *testing.T) {
	type item struct {
		Value int
		Array [3]int
	}
	type root struct {
		Object  *item
		Field   *int
		Array   []int
		Element *int
		Keys    map[*int]string
	}
	r := newFramedTestRegistry([]reflect.Type{reflect.TypeFor[root](), reflect.TypeFor[item]()}, nil, nil)
	object := &item{Value: 4, Array: [3]int{1, 2, 3}}
	input := &root{Object: object, Field: &object.Value, Array: object.Array[:], Element: &object.Array[1], Keys: map[*int]string{&object.Array[1]: "found"}}
	data, err := r.EncodeFramed(input)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.(*root)
	*got.Field = 9
	*got.Element = 7
	if got.Object.Value != 9 || got.Object.Array[1] != 7 || got.Array[1] != 7 || got.Keys[&got.Object.Array[1]] != "found" {
		t.Fatal("field aliases lost")
	}
}
func TestFramedGraphKeepsDistinctZeroSizePointerKeys(t *testing.T) {
	type marker struct{}
	type padded struct {
		Value   marker
		Padding byte
	}
	type root struct {
		Left, Right *marker
		Keys        map[*marker]string
	}
	storage := new([2]padded)
	left, right := &storage[0].Value, &storage[1].Value
	if left == right {
		t.Fatal("fixture marker addresses overlap")
	}
	r := newFramedTestRegistry([]reflect.Type{reflect.TypeFor[root]()}, nil, nil)
	data, err := r.EncodeFramed(&root{Left: left, Right: right, Keys: map[*marker]string{left: "left", right: "right"}})
	if err != nil {
		t.Fatal(err)
	}
	value, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := value.(*root)
	if got.Left == got.Right || got.Keys[got.Left] != "left" || got.Keys[got.Right] != "right" {
		t.Fatal("distinct zero-size identities collapsed")
	}
}
func TestFramedGraphPreservesNamedByteSlices(t *testing.T) {
	type edge uint8
	type root struct{ Edges []edge }
	r := newFramedTestRegistry([]reflect.Type{reflect.TypeFor[root]()}, nil, nil)
	input := &root{Edges: []edge{0, 1, 2, 255}}
	data, err := r.EncodeFramed(input)
	if err != nil {
		t.Fatal(err)
	}
	value, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, value) {
		t.Fatal("named-byte slice changed")
	}
}
func TestFramedInterfaceReferencesPreserveTypedNilsAndAliases(t *testing.T) {
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
	r := newFramedTestRegistry([]reflect.Type{reflect.TypeFor[root](), reflect.TypeFor[graph]()}, nil, nil)
	data, err := r.EncodeFramed(original)
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
func TestFramedMapDecodeScratchDoesNotLeakBetweenEntries(t *testing.T) {
	type root struct{ Maps []map[string]mapDecodeEntry }
	node := &graph{Name: "shared"}
	original := &root{Maps: []map[string]mapDecodeEntry{
		{"full": {Label: "first", Data: []byte{1, 2}, Link: node, Dynamic: "dynamic", Number: 19}, "zero": {}},
		{"next": {Number: 7}, "empty": {}, "shared": {Link: node}},
	}}
	r := newFramedTestRegistry([]reflect.Type{reflect.TypeFor[root](), reflect.TypeFor[graph]()}, nil, nil)
	data, err := r.EncodeFramed(original)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		decoded, err := r.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		got := decoded.(*root)
		if !reflect.DeepEqual(original, got) {
			t.Fatal("map scratch leaked values or changed entries")
		}
		if got.Maps[0]["full"].Link != got.Maps[1]["shared"].Link {
			t.Fatal("shared map entry pointer identity lost")
		}
	}
}
func TestFramedLargeGraphBatchedPointersAndInteriorAliases(t *testing.T) {
	const count = 10000
	root := &batchedGraph{Nodes: make([]*batchedNode, count), Integers: make([]namedPointer, count)}
	for i := range root.Nodes {
		root.Nodes[i] = &batchedNode{Index: i, Data: [3]int{i, i + 1, i + 2}}
		root.Integers[i] = new(int)
		*root.Integers[i] = i
	}
	for i, node := range root.Nodes {
		node.Next = root.Nodes[max(0, i-1)]
	}
	root.Interior = &root.Nodes[8180].Data[1]
	root.Tail = root.Nodes[8180].Data[1:]
	r := newFramedTestRegistry([]reflect.Type{reflect.TypeFor[batchedGraph]()}, nil, nil)
	data, err := r.EncodeFramed(root)
	if err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		value, err := r.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		got := value.(*batchedGraph)
		for i, node := range got.Nodes {
			if node.Index != i || node.Next != got.Nodes[max(0, i-1)] || node.Data[2] != i+2 || *got.Integers[i] != i {
				t.Fatalf("node %d lost value, named type or cyclic reference", i)
			}
			if i > 0 && (node == got.Nodes[i-1] || got.Integers[i] == got.Integers[i-1]) {
				t.Fatalf("distinct pointer identities merged at %d", i)
			}
		}
		if got.Interior != &got.Nodes[8180].Data[1] || &got.Tail[0] != got.Interior || cap(got.Tail) != 2 {
			t.Fatal("typed interior aliases no longer point into their owner")
		}
		*got.Interior = 19
		if got.Tail[0] != 19 || root.Nodes[8180].Data[1] != 8181 {
			t.Fatal("interior mutation lost aliasing or changed original storage")
		}
		*got.Interior = 8181

		data, err = r.EncodeFramed(got)
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestFramedBatchedSlicesKeepCapacitiesHiddenValuesAndAliases(t *testing.T) {
	const count = 50000
	root := &sliceBatchGraph{Slices: make([]batchNamedSlice, count)}
	for i := range root.Slices {
		full := make(batchNamedSlice, 4+i%3)
		for j := range full {
			full[j] = i*4 + j
		}
		root.Slices[i] = full[:i%3]
	}
	root.Slices[1][0] = root.Slices[1]
	full := root.Slices[8180][:4]
	root.Interior = &full[3]
	root.Tail = []any(full[1:4])
	r := newFramedTestRegistry([]reflect.Type{reflect.TypeFor[sliceBatchGraph]()}, nil, nil)
	RegisterTypedSlice[batchNamedSlice](r)
	data, err := r.EncodeFramed(root)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		decoded, err := r.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		got := decoded.(*sliceBatchGraph)
		runtime.GC()
		if !reflect.DeepEqual(root, got) {
			t.Fatal("batched slice graph changed")
		}
		for i, s := range got.Slices {
			if len(s) != i%3 || cap(s) != 4+i%3 {
				t.Fatalf("slice %d boundaries changed", i)
			}
			if i != 1 && !reflect.DeepEqual([]any(root.Slices[i][:cap(root.Slices[i])]), []any(s[:cap(s)])) {
				t.Fatal("hidden capacity values lost")
			}
		}
		*got.Interior = "changed"
		if got.Tail[2] != "changed" || got.Slices[8180][:4][3] != "changed" {
			t.Fatal("slice interior alias lost")
		}
		got.Slices[1][0].(batchNamedSlice)[0] = "cycle"
		if got.Slices[1][0] != "cycle" {
			t.Fatal("named slice cycle lost")
		}
		next := append([]any(nil), got.Slices[1000][:cap(got.Slices[1000])]...)
		got.Slices[999] = append(got.Slices[999], 1, 2, 3, 4, 5)
		if !reflect.DeepEqual(next, []any(got.Slices[1000][:cap(got.Slices[1000])])) {
			t.Fatal("append overwrote adjacent batched slice")
		}

		data, err = r.EncodeFramed(root)
		if err != nil {
			t.Fatal(err)
		}
		if round == 0 {
			fresh, err := r.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			data, err = r.EncodeFramed(fresh)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
