package statecache

import (
	"crypto/sha256"
	"encoding/binary"
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

func TestMapDecodeRejectsDuplicateKeys(t *testing.T) {
	r := New([]reflect.Type{reflect.TypeFor[map[string]int]()}, nil, nil)
	data, err := r.Encode(map[string]int{"aa": 1, "bb": 2})
	if err != nil {
		t.Fatal(err)
	}
	readUint := func(at *int) uint64 {
		t.Helper()
		value, n := binary.Uvarint(data[*at:])
		if n <= 0 {
			t.Fatal("invalid encoded map")
		}
		*at += n
		return value
	}
	at := 9 + sha256.Size
	readUint(&at) // root type
	if objects := readUint(&at); objects != 1 {
		t.Fatalf("encoded object count = %d, want 1", objects)
	}
	readUint(&at) // map type
	if entries := readUint(&at); entries != 2 {
		t.Fatalf("encoded map length = %d, want 2", entries)
	}
	if owner := readUint(&at); owner != 0 {
		t.Fatalf("encoded map owner = %d, want 0", owner)
	}
	readKey := func() (int, []byte) {
		t.Helper()
		code := readUint(&at)
		if code&1 == 0 || code>>1 != 2 {
			t.Fatalf("expected a two-byte literal key, got code %d", code)
		}
		start := at
		at += 2
		return start, data[start:at]
	}
	_, firstKey := readKey()
	readUint(&at) // first value
	secondStart, _ := readKey()
	readUint(&at) // second value
	copy(data[secondStart:secondStart+2], firstKey)
	if root := readUint(&at); root != 1 {
		t.Fatalf("encoded root reference = %d, want 1", root)
	}
	if at != len(data)-sha256.Size {
		t.Fatal("unexpected trailing map encoding")
	}
	digest := sha256.Sum256(data[:len(data)-sha256.Size])
	copy(data[len(data)-sha256.Size:], digest[:])
	if _, err := r.Decode(data); err == nil {
		t.Fatal("accepted duplicate map keys")
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
