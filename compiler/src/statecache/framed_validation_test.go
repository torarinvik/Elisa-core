package statecache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func framedChecksum(payload []byte) []byte {
	digest := sha256.Sum256(payload)
	return append(payload, digest[:]...)
}

func TestFramedRejectsMalformedFramesAndDictionary(t *testing.T) {
	r := New([]reflect.Type{reflect.TypeFor[string]()}, nil, nil)
	data, err := r.EncodeFramed("saved source")
	if err != nil {
		t.Fatal(err)
	}
	_, n := binary.Uvarint(data[41:])
	header := 41 + n
	count, n := binary.Uvarint(data[header:])
	header += n
	if count != 0 {
		t.Fatal("scalar root unexpectedly has objects")
	}
	dictionary := int(binary.LittleEndian.Uint64(data[header:]))
	body := header + 16
	cases := map[string]func([]byte){
		"dictionary before bodies":   func(p []byte) { binary.LittleEndian.PutUint64(p[header:], uint64(body-1)) },
		"dictionary outside payload": func(p []byte) { binary.LittleEndian.PutUint64(p[header:], uint64(len(p)+1)) },
		"frame overflow":             func(p []byte) { binary.LittleEndian.PutUint64(p[header+8:], ^uint64(0)) },
		"unfilled bodies":            func(p []byte) { binary.LittleEndian.PutUint64(p[header+8:], 0) },
		"invalid string ID":          func(p []byte) { p[body] = 127 },
		"empty dictionary entry":     func(p []byte) { p[dictionary+1] = 0 },
		"missing dictionary":         func(p []byte) { p[dictionary] = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			payload := append([]byte(nil), data[:len(data)-32]...)
			mutate(payload)
			if v, err := r.Decode(framedChecksum(payload)); err == nil || v != nil {
				t.Fatal("accepted malformed framed state")
			}
		})
	}
	for end := 41; end < len(data)-32; end++ {
		payload := append([]byte(nil), data[:end]...)
		if v, err := r.Decode(framedChecksum(payload)); err == nil || v != nil {
			t.Fatalf("accepted truncated payload %d", end)
		}
	}
	payload := append(append([]byte(nil), data[:len(data)-32]...), 0)
	if _, err := r.Decode(framedChecksum(payload)); err == nil {
		t.Fatal("accepted trailing dictionary data")
	}
}

func TestFramedParallelBudgetAndRepeatedDecode(t *testing.T) {
	type row struct {
		Name   string
		Values map[string]any
	}
	root := make([]*row, 10000)
	for i := range root {
		root[i] = &row{Name: "shared name", Values: map[string]any{"number": i, "text": "shared value"}}
	}
	r := New([]reflect.Type{reflect.TypeFor[[]*row]()}, nil, nil)
	r.EnableParallelDecode()
	data, err := r.EncodeFramed(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []uint64{1, 1024, 100000, 1500000, 3000000, 4000000} {
		v, err := r.DecodeWithLimit(data, limit)
		if err != nil && v != nil {
			t.Fatal("partial graph published on failed budget")
		}
	}
	for repeat := 0; repeat < 3; repeat++ {
		v, err := r.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(v, root) {
			t.Fatal("parallel decode changed values")
		}
	}
}

func BenchmarkFramedObjectDecode(b *testing.B) {
	type row struct {
		Name    string
		Numbers [7]int
		Next    *row
	}
	root := make([]*row, 10000)
	for i := range root {
		root[i] = &row{Name: "shared source file", Numbers: [7]int{i, 2, 3, 4, 5, 6, 7}}
		if i > 0 {
			root[i].Next = root[i-1]
		}
	}
	serial := New([]reflect.Type{reflect.TypeFor[[]*row]()}, nil, nil)
	parallel := New([]reflect.Type{reflect.TypeFor[[]*row]()}, nil, nil)
	parallel.EnableParallelDecode()
	legacy, err := serial.Encode(root)
	if err != nil {
		b.Fatal(err)
	}
	framed, err := serial.EncodeFramed(root)
	if err != nil {
		b.Fatal(err)
	}
	for _, mode := range []struct {
		name string
		r    *Registry
		data []byte
	}{{"legacy", serial, legacy}, {"framed-serial", serial, framed}, {"framed-parallel", parallel, framed}} {
		b.Run(mode.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := mode.r.Decode(mode.data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestFramedAliasCannotOwnBytes(t *testing.T) {
	type root struct {
		Data    [4]int
		Pointer *int
		Tail    []int
	}
	value := &root{Data: [4]int{1, 2, 3, 4}}
	value.Pointer = &value.Data[1]
	value.Tail = value.Data[1:]
	r := New([]reflect.Type{reflect.TypeFor[*root]()}, nil, nil)
	data, err := r.EncodeFramed(value)
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte(nil), data[:len(data)-32]...)
	// This graph has no strings, so its dictionary is the final zero count.
	var word [8]byte
	binary.LittleEndian.PutUint64(word[:], uint64(len(payload)-1))
	if bytes.Count(payload, word[:]) != 1 {
		t.Fatal("ambiguous test framing offset")
	}
	header := bytes.Index(payload, word[:])
	at := 41
	_, n := binary.Uvarint(payload[at:])
	at += n
	count, _ := binary.Uvarint(payload[at:])
	found := false
	for i := 0; i < int(count); i++ {
		slot := payload[header+8*(i+1):]
		if binary.LittleEndian.Uint64(slot) == 0 {
			binary.LittleEndian.PutUint64(slot, 1)
			found = true
			break
		}
	}
	if !found {
		t.Fatal("test graph has no alias frame")
	}
	if v, err := r.Decode(framedChecksum(payload)); err == nil || v != nil {
		t.Fatal("alias body accepted")
	}
}

func TestSharedDecodeBudgetCannotUnderflow(t *testing.T) {
	var budget atomic.Uint64
	budget.Store(16)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := decoder{sharedBudget: &budget}
			if d.reserve(1, 8) == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 2 || budget.Load() != 0 {
		t.Fatalf("budget escaped bound: successes=%d remaining=%d", successes.Load(), budget.Load())
	}
	d := decoder{sharedBudget: &budget}
	if d.reserve(^uint64(0), 8) == nil {
		t.Fatal("multiplication overflow accepted")
	}
	if d.reserve(^uint64(0), 0) != nil || budget.Load() != 0 {
		t.Fatal("zero-size reservation changed budget")
	}
}

func TestFramedStringsOwnBytesAndChecksum(t *testing.T) {
	r := New([]reflect.Type{reflect.TypeFor[string]()}, nil, nil)
	data, err := r.EncodeFramed("immutable copied dictionary")
	if err != nil {
		t.Fatal(err)
	}
	value, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := range data {
		copy := append([]byte(nil), data...)
		copy[i] ^= 255
		if _, err := r.Decode(copy); err == nil {
			t.Fatalf("accepted corrupt byte %d", i)
		}
	}
	for i := range data {
		data[i] = 0
	}
	if value.(string) != "immutable copied dictionary" {
		t.Fatal("restored string aliases input bytes")
	}
}

func TestFramedCustomAccessorRequiresParallelAssertion(t *testing.T) {
	type node struct {
		Name   string
		Number int
	}
	root := make([]*node, 5000)
	for i := range root {
		root[i] = &node{Name: "name", Number: i + 1}
	}
	var active atomic.Int32
	var overlap atomic.Bool
	access := func(v reflect.Value, i int) reflect.Value {
		if active.Add(1) > 1 {
			overlap.Store(true)
		}
		runtime.Gosched()
		field := v.Field(i)
		active.Add(-1)
		return field
	}
	r := New([]reflect.Type{reflect.TypeFor[[]*node]()}, nil, access)
	data, err := r.EncodeFramed(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Decode(data); err != nil {
		t.Fatal(err)
	}
	if overlap.Load() {
		t.Fatal("custom accessor called concurrently without assertion")
	}
	r.EnableParallelDecode()
	if _, err := r.Decode(data); err != nil {
		t.Fatal(err)
	}
	if runtime.GOMAXPROCS(0) > 1 && !overlap.Load() {
		t.Fatal("asserted accessor did not use parallel workers")
	}
}
