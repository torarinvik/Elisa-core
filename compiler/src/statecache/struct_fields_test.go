package statecache

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"
)

func TestStateStructFieldsRequireCanonicalAllowedOrder(t *testing.T) {
	type record struct{ First, Excluded, Last uint64 }
	typ := reflect.TypeFor[record]()
	r := New([]reflect.Type{typ}, func(_ reflect.Type, name string) bool { return name != "Excluded" }, nil)
	cases := []struct {
		name  string
		words []uint64
		valid bool
		want  record
	}{
		{"empty", []uint64{0}, true, record{}},
		{"both", []uint64{2, 0, 7, 2, 9}, true, record{First: 7, Last: 9}},
		{"omitted first", []uint64{1, 2, 9}, true, record{Last: 9}},
		{"duplicate", []uint64{2, 0, 7, 0, 9}, false, record{}},
		{"descending", []uint64{2, 2, 9, 0, 7}, false, record{}},
		{"excluded", []uint64{1, 1, 7}, false, record{}},
		{"unknown", []uint64{1, 3, 7}, false, record{}},
		{"overflow", []uint64{1, ^uint64(0), 7}, false, record{}},
		{"truncated value", []uint64{1, 2}, false, record{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var data []byte
			for _, word := range tc.words {
				data = binary.AppendUvarint(data, word)
			}
			d := decoder{registry: r, data: data, budget: 1 << 20}
			value := reflect.New(typ).Elem()
			err := d.value(value)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.valid && value.Interface().(record) != tc.want {
				t.Fatalf("got %+v want %+v", value.Interface(), tc.want)
			}
		})
	}
}

func TestStateStructFieldsBeyondWordBoundary(t *testing.T) {
	fields := make([]reflect.StructField, 130)
	for i := range fields {
		fields[i] = reflect.StructField{Name: fmt.Sprintf("Field%d", i), Type: reflect.TypeFor[uint64]()}
	}
	typ := reflect.StructOf(fields)
	r := New([]reflect.Type{typ}, nil, nil)
	value := reflect.New(typ).Elem()
	value.Field(0).SetUint(7)
	value.Field(64).SetUint(11)
	value.Field(129).SetUint(19)
	data, err := r.Encode(value.Interface())
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Decode(data)
	if err != nil || !reflect.DeepEqual(got, value.Interface()) {
		t.Fatalf("large struct mismatch: %v", err)
	}
}

type fieldBenchmarkRecord struct {
	A, B, C, D, E, F, G, H, I, J, K, L, M, N, O, P uint64
}

func BenchmarkStateStructFields(b *testing.B) {
	values := make([]fieldBenchmarkRecord, 10000)
	for i := range values {
		values[i] = fieldBenchmarkRecord{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	}
	r := New([]reflect.Type{reflect.TypeOf(values)}, nil, nil)
	data, err := r.Encode(values)
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
