package statecache

import (
	"bytes"
	"reflect"
	"testing"
)

func TestInventoryPruningPreservesWireAndInteriorAliases(t *testing.T) {
	type scalar struct {
		File  string
		Line  int
		Flags [8]uint64
	}
	type root struct {
		Rows []scalar
		Name *string
		Line *int
	}
	rows := make([]scalar, 1000)
	for i := range rows {
		rows[i] = scalar{File: "compiler.elisa", Line: i, Flags: [8]uint64{1, 2, 3}}
	}
	input := &root{Rows: rows, Name: &rows[417].File, Line: &rows[619].Line}
	r := New([]reflect.Type{reflect.TypeFor[root]()}, nil, nil)
	// Re-enable the former complete inventory walk without changing the writer.
	reference := *r
	reference.scanFields = r.fields
	reference.hasObjects = map[reflect.Type]bool{}
	for _, typ := range r.types {
		reference.hasObjects[typ] = true
	}
	legacy, err := reference.Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	data, err := r.Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacy, data) {
		t.Fatal("pruning changed object IDs, aliases or encoded values")
	}
	value, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := value.(*root)
	*got.Name = "changed"
	*got.Line = 23
	if got.Rows[417].File != "changed" || got.Rows[619].Line != 23 {
		t.Fatal("pointer into a scalar-only owner lost its alias")
	}
}

func TestInventoryPruningStillRejectsProcessLocalValues(t *testing.T) {
	type scalar struct {
		Callback func()
		Address  uintptr
	}
	type root struct {
		Rows   []scalar
		Values map[string]scalar
	}
	r := New([]reflect.Type{reflect.TypeFor[root]()}, nil, nil)
	for _, input := range []*root{{Rows: []scalar{{Callback: func() {}}}}, {Values: map[string]scalar{"bad": {Address: 123}}}} {
		if _, err := r.Encode(input); err == nil {
			t.Fatal("pruned inventory bypassed final value validation")
		}
	}
}
