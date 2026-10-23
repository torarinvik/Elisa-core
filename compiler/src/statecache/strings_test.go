package statecache

import (
	"bytes"
	"reflect"
	"testing"
)

func TestStateStringsDeduplicateByContent(t *testing.T) {
	r, root := repeatedStringFixture()
	data, err := r.Encode(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 100000 {
		t.Fatalf("repeated strings were not compacted: %d bytes", len(data))
	}
	if count := bytes.Count(data, []byte(root[0].File)); count != 1 {
		t.Fatalf("filename encoded %d times", count)
	}
	value, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := value.([]stringEntry)
	if !reflect.DeepEqual(root, got) {
		t.Fatal("string values changed")
	}
	for i := range data {
		data[i] = 0
	}
	if !reflect.DeepEqual(root, got) {
		t.Fatal("restored strings alias mutable input bytes")
	}
}

func TestStateStringsKeepEmptyAndBinaryBytes(t *testing.T) {
	type namedString string
	root := []namedString{"", "a\x00b", "\xff\x00\xfd", "a\x00b", ""}
	r := New([]reflect.Type{reflect.TypeFor[[]namedString]()}, nil, nil)
	data, err := r.Encode(root)
	if err != nil {
		t.Fatal(err)
	}
	value, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(root, value) {
		t.Fatal("binary or named string changed")
	}
}

func TestStateStringRejectsMalformedReferencesAndBudgets(t *testing.T) {
	for _, data := range [][]byte{{2}, {4}, {1}, {7, 'a'}, {255, 255, 255}} {
		d := decoder{data: data, budget: 1024, strings: []string{""}}
		if _, err := d.stateString(); err == nil {
			t.Fatalf("accepted malformed string %v", data)
		}
	}
	// The literal x costs one byte plus one dictionary slot. A backreference
	// costs no additional decoded string storage.
	for _, budget := range []uint64{0, uint64(reflect.TypeFor[string]().Size())} {
		d := decoder{data: []byte{3, 'x'}, budget: budget, strings: []string{""}}
		if _, err := d.stateString(); err == nil {
			t.Fatal("string allocation escaped budget", budget)
		}
	}
	d := decoder{data: []byte{3, 'x', 2, 0}, budget: uint64(1 + reflect.TypeFor[string]().Size()), strings: []string{""}}
	for _, want := range []string{"x", "x", ""} {
		got, err := d.stateString()
		if err != nil || got != want {
			t.Fatal(got, err, want)
		}
	}
	if d.budget != 0 {
		t.Fatal("unexpected string allocation accounting", d.budget)
	}
}
