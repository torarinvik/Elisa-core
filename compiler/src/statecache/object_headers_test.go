package statecache

import (
	"reflect"
	"testing"
	"unsafe"
)

type namedPointer *int
type namedMap map[string]any
type namedSlice []any

type namedReferences struct {
	Pointer namedPointer
	Alias   namedPointer
	Map     namedMap
	Slice   namedSlice
}

func TestNamedObjectReferencesRetainIdentity(t *testing.T) {
	root := &namedReferences{Pointer: new(int), Map: namedMap{}, Slice: make(namedSlice, 1, 2)}
	root.Alias = root.Pointer
	*root.Pointer = 42
	root.Map["self"] = root.Map
	root.Slice[0] = root.Slice
	r := New([]reflect.Type{reflect.TypeFor[namedReferences]()}, nil, nil)
	data, err := r.Encode(root)
	if err != nil {
		t.Fatal(err)
	}
	value, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := value.(*namedReferences)
	if *got.Pointer != 42 || got.Pointer != got.Alias || got.Pointer == root.Pointer {
		t.Fatal("named pointer identity or ownership lost")
	}
	*got.Alias = 7
	if *got.Pointer != 7 {
		t.Fatal("named pointer alias not writable")
	}
	got.Map["self"].(namedMap)["new"] = 8
	if got.Map["new"] != 8 {
		t.Fatal("named map cycle lost")
	}
	got.Slice[0].(namedSlice)[0] = "changed"
	if got.Slice[0] != "changed" || cap(got.Slice) != 2 {
		t.Fatal("named slice cycle or capacity lost")
	}
	if *root.Pointer != 42 || len(root.Map) != 1 {
		t.Fatal("decode retained input graph storage")
	}
}

type pointerHeaderEntry struct {
	Name string
	Next *pointerHeaderEntry
}

func TestObjectDescriptionUsesWideFallbackOnlyWhenNeeded(t *testing.T) {
	d := &decoder{budget: 1 << 20}
	compact, err := d.describeObject(1, 9, 7, 7, 7)
	if err != nil {
		t.Fatal(err)
	}
	if compact.typeID != 9 || compact.sizes != 7|(7<<16) {
		t.Fatalf("compact descriptor = %+v", compact)
	}
	if got, want := unsafe.Sizeof(compact), unsafe.Sizeof(uintptr(0))+8; got != want {
		t.Fatalf("object descriptor size = %d, want %d", got, want)
	}
	if typeID, length, capacity, ok := d.objectDetails(1, compact); !ok || typeID != 9 || length != 7 || capacity != 7 {
		t.Fatalf("compact descriptor details = %d, %d, %d, %v", typeID, length, capacity, ok)
	}

	for _, dimensions := range []struct {
		id, typeID       uint64
		length, capacity int
	}{{2, 11, 8, 9}, {2, 11, 7, 8}, {3, uint64(^uint32(0)) + 1, 7, 7}} {
		wide, err := d.describeObject(int(dimensions.id), dimensions.typeID, dimensions.length, dimensions.capacity, 7)
		if err != nil {
			t.Fatal(err)
		}
		if wide.typeID != 0 {
			t.Fatalf("wide descriptor retained compact type ID: %+v", wide)
		}
		typeID, length, capacity, ok := d.objectDetails(int(dimensions.id), wide)
		if !ok || typeID != dimensions.typeID || length != dimensions.length || capacity != dimensions.capacity {
			t.Fatalf("wide descriptor details = %d, %d, %d, %v", typeID, length, capacity, ok)
		}
	}
}

func BenchmarkStatePointerObjects(b *testing.B) {
	root := make([]*pointerHeaderEntry, 10000)
	for i := range root {
		root[i] = &pointerHeaderEntry{Name: "entry"}
		if i > 0 {
			root[i].Next = root[i-1]
		}
	}
	r := New([]reflect.Type{reflect.TypeFor[[]*pointerHeaderEntry]()}, nil, nil)
	data, err := r.Encode(root)
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

type batchedNode struct {
	Index int
	Next  *batchedNode
	Data  [3]int
}
type batchedGraph struct {
	Nodes    []*batchedNode
	Integers []namedPointer
	Interior *int
	Tail     []int
}

func TestLargeGraphBatchedPointersAndInteriorAliases(t *testing.T) {
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
	r := New([]reflect.Type{reflect.TypeFor[batchedGraph]()}, nil, nil)
	data, err := r.Encode(root)
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
		// Batching must remain an allocation detail. The restored graph can be
		// encoded and decoded again using ordinary typed owner/alias rules.
		data, err = r.Encode(got)
		if err != nil {
			t.Fatal(err)
		}
	}
}
