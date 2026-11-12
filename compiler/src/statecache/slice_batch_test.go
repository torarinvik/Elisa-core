package statecache

import (
	"reflect"
	"runtime"
	"testing"
)

type batchNamedSlice []any
type sliceBatchGraph struct {
	Slices   []batchNamedSlice
	Interior *any
	Tail     []any
}

func TestBatchedSlicesKeepCapacitiesHiddenValuesAndAliases(t *testing.T) {
	const count = 50000
	root := &sliceBatchGraph{Slices: make([]batchNamedSlice, count)}
	for i := range root.Slices {
		full := make(batchNamedSlice, 4+i%3)
		for j := range full {
			full[j] = i*4 + j
		}
		root.Slices[i] = full[:i%3]
	}
	root.Slices[1][0] = root.Slices[1]
	full := root.Slices[8180][:4]
	root.Interior = &full[3]
	root.Tail = []any(full[1:4])
	r := New([]reflect.Type{reflect.TypeFor[sliceBatchGraph]()}, nil, nil)
	RegisterTypedSlice[batchNamedSlice](r)
	data, err := r.Encode(root)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		decoded, err := r.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		got := decoded.(*sliceBatchGraph)
		runtime.GC()
		if !reflect.DeepEqual(root, got) {
			t.Fatal("batched slice graph changed")
		}
		for i, s := range got.Slices {
			if len(s) != i%3 || cap(s) != 4+i%3 {
				t.Fatalf("slice %d boundaries changed", i)
			}
			if i != 1 && !reflect.DeepEqual([]any(root.Slices[i][:cap(root.Slices[i])]), []any(s[:cap(s)])) {
				t.Fatal("hidden capacity values lost")
			}
		}
		*got.Interior = "changed"
		if got.Tail[2] != "changed" || got.Slices[8180][:4][3] != "changed" {
			t.Fatal("slice interior alias lost")
		}
		got.Slices[1][0].(batchNamedSlice)[0] = "cycle"
		if got.Slices[1][0] != "cycle" {
			t.Fatal("named slice cycle lost")
		}
		next := append([]any(nil), got.Slices[1000][:cap(got.Slices[1000])]...)
		got.Slices[999] = append(got.Slices[999], 1, 2, 3, 4, 5)
		if !reflect.DeepEqual(next, []any(got.Slices[1000][:cap(got.Slices[1000])])) {
			t.Fatal("append overwrote adjacent batched slice")
		}
		// Re-encoding batched physical storage must preserve logical slice identities.
		data, err = r.Encode(root)
		if err != nil {
			t.Fatal(err)
		}
		if round == 0 {
			fresh, err := r.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			data, err = r.Encode(fresh)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSliceIndexPreservesShapesAcrossGrowth(t *testing.T) {
	type sliceGraph struct {
		Owner []int
		Views [][]int
	}
	root := &sliceGraph{Owner: []int{10, 20, 30, 40}}
	root.Views = append(root.Views, root.Owner[:0:2], root.Owner[:1:3], nil, []int{})
	for i := range 5000 {
		root.Views = append(root.Views, []int{i})
	}
	root.Views = append(root.Views, root.Owner[:1:3])
	registry := New([]reflect.Type{reflect.TypeFor[sliceGraph]()}, nil, nil)
	registry.EnableParallelDecode()
	for _, encode := range []func(any) ([]byte, error){registry.Encode, registry.EncodeFramed} {
		data, err := encode(root)
		if err != nil {
			t.Fatal(err)
		}
		result, err := registry.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		got := result.(*sliceGraph)
		if len(got.Views[0]) != 0 || cap(got.Views[0]) != 2 ||
			len(got.Views[1]) != 1 || cap(got.Views[1]) != 3 ||
			got.Views[2] != nil || got.Views[3] == nil {
			t.Fatal("slice boundaries or nilness changed")
		}
		got.Views[0][:2][1] = 99
		if got.Owner[1] != 99 || got.Views[1][:3][1] != 99 ||
			got.Views[len(got.Views)-1][:3][1] != 99 {
			t.Fatal("slice backing alias lost after index growth")
		}
		for i, view := range got.Views[4 : len(got.Views)-1] {
			if len(view) != 1 || view[0] != i {
				t.Fatalf("unrelated slice %d changed: %v", i, view)
			}
		}
	}
}

func BenchmarkStateSmallSlices(b *testing.B) {
	root := make([][]int, 10000)
	for i := range root {
		root[i] = make([]int, 2, 4)
		root[i][0] = i
		root[i][:4][3] = i + 1
	}
	r := New([]reflect.Type{reflect.TypeFor[[][]int]()}, nil, nil)
	RegisterTypedSlice[[]int](r)
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
