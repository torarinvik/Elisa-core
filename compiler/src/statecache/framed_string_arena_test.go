package statecache

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestFramedStringArenasOwnMultiChunkAndLargeStrings(t *testing.T) {
	const entryBytes = 4096
	entries := make([]string, 2*framedStringArenaChunkBytes/entryBytes+3)
	for i := range entries {
		entries[i] = fmt.Sprintf("%06d:%s", i, strings.Repeat("x", entryBytes-7))
	}
	entries = append(entries, strings.Repeat("large\x00", framedStringArenaChunkBytes/6+2))
	r := New([]reflect.Type{reflect.TypeFor[[]string]()}, nil, nil)
	data, err := r.EncodeFramed(entries)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.([]string)
	if !reflect.DeepEqual(got, entries) {
		t.Fatal("multi-chunk or oversized dictionary string changed")
	}

	// Restored strings must own immutable bytes, even after the serialized
	// payload is overwritten and garbage collection runs.
	for i := range data {
		data[i] = 0
	}
	runtime.GC()
	if !reflect.DeepEqual(got, entries) {
		t.Fatal("restored strings retained the mutable input payload")
	}
}

func appendTestDictionary(entries ...string) []byte {
	data := binary.AppendUvarint(nil, uint64(len(entries)))
	for _, entry := range entries {
		data = binary.AppendUvarint(data, uint64(len(entry)))
		data = append(data, entry...)
	}
	return data
}

func TestFramedStringArenaPreservesBudgetAndMalformedChecks(t *testing.T) {
	stringHeader := reflect.TypeFor[string]().Size()
	data := appendTestDictionary("x")
	for _, budget := range []uint64{2 * uint64(stringHeader), 2*uint64(stringHeader) + 1} {
		d := decoder{data: data, budget: budget}
		n, err := d.count()
		if err != nil {
			t.Fatal(err)
		}
		err = d.readFramedStringDictionary(n)
		if budget == 2*uint64(stringHeader) {
			if err == nil {
				t.Fatal("accepted string bytes beyond decode budget")
			}
			continue
		}
		if err != nil || d.budget != 0 || d.strings[1] != "x" {
			t.Fatalf("exact string budget failed: strings=%q remaining=%d err=%v", d.strings, d.budget, err)
		}
	}

	malformed := appendTestDictionary(strings.Repeat("a", framedStringArenaChunkBytes), "")
	d := decoder{data: malformed, budget: 2*uint64(stringHeader) + framedStringArenaChunkBytes}
	n, err := d.count()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.readFramedStringDictionary(n); err == nil {
		t.Fatal("accepted an empty dictionary entry after an arena boundary")
	}

	trailing := append(appendTestDictionary("valid"), 0)
	d = decoder{data: trailing, budget: 2*uint64(stringHeader) + 5}
	n, err = d.count()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.readFramedStringDictionary(n); err == nil {
		t.Fatal("accepted trailing dictionary data")
	}
}
