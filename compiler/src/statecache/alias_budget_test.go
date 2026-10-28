package statecache

import (
	"crypto/sha256"
	"encoding/binary"
	"reflect"
	"testing"
	"unsafe"
)

func TestDecodeWithLimitChargesOneAliasSlab(t *testing.T) {
	type node struct{ Value int }
	type root struct {
		Owner node
		Alias *node
	}
	value := &root{Owner: node{Value: 7}}
	value.Alias = &value.Owner
	r := New([]reflect.Type{reflect.TypeFor[*root]()}, nil, nil)
	data, err := r.EncodeFramed(value)
	if err != nil {
		t.Fatal(err)
	}

	minimum := minimumSuccessfulDecodeBudget(t, r, data)
	objectCount := encodedStateObjectCount(t, data)
	slabBytes := min(uint64(decodedAliasChunkSize), objectCount) * uint64(unsafe.Sizeof(nodeAlias{}))
	if minimum < slabBytes {
		t.Fatalf("minimum decode budget %d does not cover one alias slab (%d bytes)", minimum, slabBytes)
	}
	if _, err := r.DecodeWithLimit(data, minimum-1); err == nil {
		t.Fatal("decode succeeded below the minimum allocation budget")
	}
	decoded, err := r.DecodeWithLimit(data, minimum)
	if err != nil {
		t.Fatalf("decode failed at exact minimum budget %d: %v", minimum, err)
	}
	got := decoded.(*root)
	if got.Alias != &got.Owner || got.Alias.Value != 7 {
		t.Fatal("tight-budget decode lost the interior pointer alias")
	}
}

func TestDecodeWithLimitChargesSecondAliasSlabAtBoundary(t *testing.T) {
	type node struct{ Value int }
	type root struct {
		Nodes    []node
		Pointers []*node
	}
	const aliases = decodedAliasChunkSize + 1
	value := &root{Nodes: make([]node, aliases), Pointers: make([]*node, aliases)}
	for i := range value.Nodes {
		value.Nodes[i].Value = i
		value.Pointers[i] = &value.Nodes[i]
	}
	r := New([]reflect.Type{reflect.TypeFor[*root]()}, nil, nil)
	data, err := r.EncodeFramed(value)
	if err != nil {
		t.Fatal(err)
	}

	minimum := minimumSuccessfulDecodeBudget(t, r, data)
	objectCount := encodedStateObjectCount(t, data)
	firstSlab := min(uint64(decodedAliasChunkSize), objectCount)
	secondSlab := min(uint64(decodedAliasChunkSize), objectCount-firstSlab)
	slabBytes := (firstSlab + secondSlab) * uint64(unsafe.Sizeof(nodeAlias{}))
	if minimum < slabBytes {
		t.Fatalf("minimum decode budget %d does not cover two alias slabs (%d bytes)", minimum, slabBytes)
	}
	if _, err := r.DecodeWithLimit(data, minimum-1); err == nil {
		t.Fatal("decode succeeded one byte below the second-slab boundary")
	}
	decoded, err := r.DecodeWithLimit(data, minimum)
	if err != nil {
		t.Fatalf("decode failed at exact second-slab budget %d: %v", minimum, err)
	}
	got := decoded.(*root)
	for i := range got.Nodes {
		if got.Pointers[i] != &got.Nodes[i] || got.Pointers[i].Value != i {
			t.Fatalf("alias %d lost its owner at the slab boundary", i)
		}
	}
}

func TestDecodedAliasTableBoundsAndChargesSmallSlab(t *testing.T) {
	wantAliases := uint64(2)
	chunkBytes := wantAliases * uint64(unsafe.Sizeof(nodeAlias{}))
	directoryBytes := uint64(unsafe.Sizeof([]nodeAlias{}))
	for _, limit := range []uint64{chunkBytes + directoryBytes - 1, chunkBytes + directoryBytes} {
		d := &decoder{budget: limit}
		aliases := decodedAliasTable{maxAliases: wantAliases}
		_, err := aliases.append(d, nodeAlias{})
		if limit < chunkBytes+directoryBytes {
			if err == nil || aliases.capacity() != 0 {
				t.Fatalf("alias slab allocation exceeded limit %d: err=%v capacity=%d", limit, err, aliases.capacity())
			}
			continue
		}
		if err != nil {
			t.Fatalf("alias slab allocation failed at exact budget %d: %v", limit, err)
		}
		if got := aliases.capacity(); got != wantAliases {
			t.Fatalf("small slab capacity = %d, want %d", got, wantAliases)
		}
		if d.budget != 0 {
			t.Fatalf("remaining alias-table budget = %d, want 0", d.budget)
		}
	}
}

func TestDecodeRejectsCyclicAliasOwners(t *testing.T) {
	type node struct{ Value int }
	type root struct {
		Nodes    []node
		Pointers []*node
	}
	value := &root{Nodes: make([]node, 2), Pointers: make([]*node, 2)}
	value.Pointers[0], value.Pointers[1] = &value.Nodes[0], &value.Nodes[1]
	r := New([]reflect.Type{reflect.TypeFor[*root]()}, nil, nil)
	data, err := r.EncodeFramed(value)
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte(nil), data[:len(data)-32]...)
	descriptors := framedObjectDescriptorOffsets(t, r, payload)
	var aliasIDs []int
	for i := range descriptors {
		if descriptors[i].ownerAt >= 0 {
			aliasIDs = append(aliasIDs, i+1)
		}
	}
	if len(aliasIDs) < 2 {
		t.Fatal("test graph has fewer than two aliases")
	}
	left, right := aliasIDs[0], aliasIDs[1]
	putSameWidthUvarint(t, payload, descriptors[left-1].ownerAt, uint64(right))
	putSameWidthUvarint(t, payload, descriptors[right-1].ownerAt, uint64(left))
	if decoded, err := r.Decode(appendChecksum(payload)); err == nil || decoded != nil {
		t.Fatal("cyclic alias-owner chain was accepted")
	}
}

func TestDecodeRejectsMalformedAliasPath(t *testing.T) {
	type node struct{ Value int }
	type root struct {
		Nodes    []node
		Pointers []*node
	}
	value := &root{Nodes: make([]node, 1), Pointers: make([]*node, 1)}
	value.Pointers[0] = &value.Nodes[0]
	r := New([]reflect.Type{reflect.TypeFor[*root]()}, nil, nil)
	data, err := r.EncodeFramed(value)
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte(nil), data[:len(data)-32]...)
	descriptors := framedObjectDescriptorOffsets(t, r, payload)
	for _, descriptor := range descriptors {
		if descriptor.ownerAt < 0 || descriptor.pathCount == 0 {
			continue
		}
		// The owner type has no field or slice element at index 63. Keep the
		// one-byte varint width unchanged so the framed-body offsets stay valid.
		payload[descriptor.pathAt] = 63<<1 | 1
		if decoded, err := r.Decode(appendChecksum(payload)); err == nil || decoded != nil {
			t.Fatal("invalid alias path was accepted")
		}
		return
	}
	t.Fatal("test graph has no path-bearing alias descriptor")
}

func minimumSuccessfulDecodeBudget(t *testing.T, r *Registry, data []byte) uint64 {
	t.Helper()
	const max = uint64(16 << 20)
	if _, err := r.DecodeWithLimit(data, max); err != nil {
		t.Fatalf("decode failed at generous budget: %v", err)
	}
	low, high := uint64(0), max
	for low < high {
		middle := low + (high-low)/2
		if _, err := r.DecodeWithLimit(data, middle); err == nil {
			high = middle
		} else {
			low = middle + 1
		}
	}
	return low
}

func encodedStateObjectCount(t *testing.T, data []byte) uint64 {
	t.Helper()
	at := 41
	_, n := binary.Uvarint(data[at:])
	if n <= 0 {
		t.Fatal("invalid root type ID in encoded test state")
	}
	at += n
	count, n := binary.Uvarint(data[at:])
	if n <= 0 {
		t.Fatal("invalid object count in encoded test state")
	}
	return count
}

type framedObjectDescriptorOffset struct {
	ownerAt   int
	pathAt    int
	pathCount uint64
}

func framedObjectDescriptorOffsets(t *testing.T, r *Registry, payload []byte) []framedObjectDescriptorOffset {
	t.Helper()
	at := 41
	read := func() uint64 {
		t.Helper()
		value, n := binary.Uvarint(payload[at:])
		if n <= 0 {
			t.Fatalf("invalid test varint at byte %d", at)
		}
		at += n
		return value
	}
	_ = read() // root type ID
	count := read()
	if count > uint64(len(payload)) {
		t.Fatal("invalid test object count")
	}
	objects := make([]framedObjectDescriptorOffset, int(count))
	for id := range objects {
		typeID := read()
		if typeID == 0 || typeID > uint64(len(r.types)) {
			t.Fatal("invalid test type ID")
		}
		typ := r.types[typeID-1]
		if typ.Kind() != reflect.Pointer {
			_ = read() // length
			if typ.Kind() == reflect.Slice {
				_ = read() // capacity
			}
		}
		ownerAt := at
		owner := read()
		objects[id].ownerAt = -1
		if owner == 0 {
			continue
		}
		objects[id].ownerAt = ownerAt
		pathCount := read()
		objects[id].pathAt = at
		objects[id].pathCount = pathCount
		for range pathCount {
			_ = read()
		}
		if typ.Kind() == reflect.Slice {
			_ = read() // alias slice offset
		}
	}
	return objects
}

func putSameWidthUvarint(t *testing.T, data []byte, at int, value uint64) {
	t.Helper()
	var encoded [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(encoded[:], value)
	_, oldWidth := binary.Uvarint(data[at:])
	if n != oldWidth {
		t.Fatalf("replacement varint width %d differs from original width %d", n, oldWidth)
	}
	copy(data[at:at+n], encoded[:n])
}

func appendChecksum(payload []byte) []byte {
	digest := sha256.Sum256(payload)
	return append(payload, digest[:]...)
}
