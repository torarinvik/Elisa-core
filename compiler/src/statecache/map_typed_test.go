package statecache

import (
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
)

type typedTestMap map[string]mapDecodeEntry

func TestTypedMapDecodeMatchesReflectiveAndBudget(t *testing.T) {
	node := &graph{Name: "shared"}
	original := typedTestMap{"full": {Label: "first", Data: []byte{1, 2}, Link: node, Dynamic: "dynamic", Number: 19}, "zero": {}, "other": {Link: node}}
	plain := New([]reflect.Type{reflect.TypeFor[typedTestMap](), reflect.TypeFor[graph]()}, nil, nil)
	typed := New([]reflect.Type{reflect.TypeFor[typedTestMap](), reflect.TypeFor[graph]()}, nil, nil)
	RegisterTypedMap[typedTestMap](typed)
	for _, framed := range []bool{false, true} {
		var data []byte
		var err error
		if framed {
			data, err = plain.EncodeFramed(original)
		} else {
			data, err = plain.Encode(original)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, limit := range []uint64{1, 128, 1024, 4096, 65536, 1 << 20} {
			a, ae := plain.DecodeWithLimit(data, limit)
			b, be := typed.DecodeWithLimit(data, limit)
			if (ae == nil) != (be == nil) {
				t.Fatalf("framed=%v limit=%d error mismatch: %v / %v", framed, limit, ae, be)
			}
			if ae == nil && (!reflect.DeepEqual(a, b) || !reflect.DeepEqual(a, original)) {
				t.Fatalf("framed=%v limit=%d values differ", framed, limit)
			}
		}
	}
}

func TestTypedMapDecodeRejectsInvalidKeys(t *testing.T) {
	for _, typed := range []bool{false, true} {
		mtype := reflect.TypeFor[map[int]int]()
		r := New([]reflect.Type{mtype}, nil, nil)
		if typed {
			RegisterTypedMap[map[int]int](r)
		}
		d := decoder{registry: r, data: []byte{2, 0, 2, 0}, budget: 1 << 20, objects: []reflect.Value{{}, reflect.MakeMap(mtype)}}
		desc := objectDescription{typeID: uint32(r.ids[mtype]), sizes: 2}
		if err := d.fillObject(1, desc); err == nil || !strings.Contains(err.Error(), "duplicate state map key") {
			t.Fatalf("typed=%v duplicate: %v", typed, err)
		}
		itype := reflect.TypeFor[map[any]int]()
		stype := reflect.TypeFor[[]int]()
		r = New([]reflect.Type{itype, stype}, nil, nil)
		if typed {
			RegisterTypedMap[map[any]int](r)
		}
		data := binary.AppendUvarint(nil, r.ids[stype])
		data = binary.AppendUvarint(data, 2)
		data = binary.AppendUvarint(data, 0)
		d = decoder{registry: r, data: data, budget: 1 << 20, objects: []reflect.Value{{}, reflect.MakeMap(itype), reflect.ValueOf([]int{1})}}
		desc = objectDescription{typeID: uint32(r.ids[itype]), sizes: 1}
		if err := d.fillObject(1, desc); err == nil || !strings.Contains(err.Error(), "noncomparable state map key") {
			t.Fatalf("typed=%v noncomparable: %v", typed, err)
		}
	}
}
