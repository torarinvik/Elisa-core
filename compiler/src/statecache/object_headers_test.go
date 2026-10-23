package statecache

import (
	"reflect"
	"testing"
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
