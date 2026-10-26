package statecache

import (
	"fmt"
	"reflect"
	"testing"
)

func TestParallelFramedEncodeRoundTrip(t *testing.T) {
	type node struct {
		Name  string
		Value int
		Next  *node
		Tags  []string
	}
	root := make([]*node, 3*parallelFramedEncodeMin)
	for i := range root {
		root[i] = &node{Name: fmt.Sprintf("n%d", i%997), Value: i, Tags: []string{"a", fmt.Sprint(i % 13)}}
		if i > 0 {
			root[i].Next = root[i-1]
		}
	}
	serial := New([]reflect.Type{reflect.TypeFor[[]*node]()}, nil, nil)
	parallel := New([]reflect.Type{reflect.TypeFor[[]*node]()}, nil, nil)
	parallel.EnableParallelDecode()
	for _, r := range []*Registry{serial, parallel} {
		data, err := r.EncodeFramed(root)
		if err != nil {
			t.Fatal(err)
		}
		value, err := serial.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		got := value.([]*node)
		if len(got) != len(root) {
			t.Fatal("length")
		}
		for i, n := range got {
			if n.Name != root[i].Name || n.Value != i || !reflect.DeepEqual(n.Tags, root[i].Tags) {
				t.Fatalf("node %d differs", i)
			}
			if i > 0 && n.Next != got[i-1] {
				t.Fatalf("node %d lost pointer identity", i)
			}
		}
	}
}

func BenchmarkParallelFramedEncode(b *testing.B) {
	type node struct {
		Value int
		Next  *node
	}
	const nodeCount = 10000
	root := make([]*node, nodeCount)
	for i := range root {
		root[i] = &node{Value: i}
		if i > 0 {
			root[i].Next = root[i-1]
		}
	}
	r := New([]reflect.Type{reflect.TypeFor[[]*node]()}, nil, nil)
	r.EnableParallelDecode() // also enables parallel framed encoding
	sample, err := r.EncodeFramed(root)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(sample)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.EncodeFramed(root); err != nil {
			b.Fatal(err)
		}
	}
}
