package statecache

import (
	"fmt"
	"reflect"
	"testing"
)

type mapDecodeEntry struct {
	Label   string
	Data    []byte
	Link    *graph
	Dynamic any
	Number  int
}

func TestMapDecodeScratchDoesNotLeakBetweenEntries(t *testing.T) {
	type root struct{ Maps []map[string]mapDecodeEntry }
	node := &graph{Name: "shared"}
	original := &root{Maps: []map[string]mapDecodeEntry{
		{"full": {Label: "first", Data: []byte{1, 2}, Link: node, Dynamic: "dynamic", Number: 19}, "zero": {}},
		{"next": {Number: 7}, "empty": {}, "shared": {Link: node}},
	}}
	r := New([]reflect.Type{reflect.TypeFor[root](), reflect.TypeFor[graph]()}, nil, nil)
	data, err := r.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	// Repeated decoding exercises either iteration order in the encoded maps.
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

func BenchmarkStateMapEntries(b *testing.B) {
	r := New([]reflect.Type{reflect.TypeFor[map[string]mapDecodeEntry]()}, nil, nil)
	original := map[string]mapDecodeEntry{}
	for i := 0; i < 10000; i++ {
		original[fmt.Sprint(i)] = mapDecodeEntry{Number: i}
	}
	data, err := r.Encode(original)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Decode(data); err != nil {
			b.Fatal(err)
		}
	}
}
