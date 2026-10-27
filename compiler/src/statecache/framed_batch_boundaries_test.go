package statecache

import (
	"encoding/binary"
	"reflect"
	"strconv"
	"testing"
)

type framedBoundaryNode struct {
	Next  *framedBoundaryNode
	Value int
}

func TestFramedDecodeAtObjectBatchBoundaries(t *testing.T) {
	for _, count := range []int{1, 63, 64, 65, 129} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			nodes := make([]framedBoundaryNode, count)
			for i := range nodes {
				nodes[i].Value = i + 17
				nodes[i].Next = &nodes[(i+1)%count]
			}

			r := New([]reflect.Type{reflect.TypeFor[framedBoundaryNode]()}, nil, nil)
			r.EnableParallelDecode()
			data, err := r.EncodeFramed(&nodes[0])
			if err != nil {
				t.Fatal(err)
			}
			at := 41
			_, n := binary.Uvarint(data[at:])
			if n <= 0 {
				t.Fatal("invalid framed type count")
			}
			at += n
			objects, n := binary.Uvarint(data[at:])
			if n <= 0 || objects != uint64(count) {
				t.Fatalf("encoded object count = %d, want %d", objects, count)
			}

			value, err := r.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			got := value.(*framedBoundaryNode)
			current := got
			for i := 0; i < count; i++ {
				if current.Value != i+17 || current.Next == nil {
					t.Fatalf("object %d lost its value or cycle", i)
				}
				current = current.Next
			}
			if current != got {
				t.Fatal("cycle changed at a frame-batch boundary")
			}
		})
	}
}
