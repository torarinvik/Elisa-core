package statecache

import (
	"reflect"
	"runtime"
	"runtime/debug"
	"testing"
)

type nonemptyPayload interface{ Number() int }
type nonemptyNode struct {
	N    int
	Next nonemptyPayload
}

func (n *nonemptyNode) Number() int {
	if n == nil {
		return -1
	}
	return n.N
}

type nonemptyMap map[string]int

func (m nonemptyMap) Number() int { return m["n"] }

type nonemptyRoot struct {
	Values []nonemptyPayload
	Node   *nonemptyNode
	Map    nonemptyMap
}

func TestNonemptyInterfaceGraphSurvivesConcurrentGC(t *testing.T) {
	old := debug.SetGCPercent(1)
	defer debug.SetGCPercent(old)
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() { close(stop); <-stopped }()
	for _, framed := range []bool{false, true} {
		node := &nonemptyNode{N: 7}
		node.Next = node
		m := nonemptyMap{"n": 2}
		source := &nonemptyRoot{Node: node, Map: m}
		for i := 0; i < 200; i++ {
			source.Values = append(source.Values, node, m, (*nonemptyNode)(nil), nonemptyMap(nil), nil)
		}
		types := []reflect.Type{reflect.TypeFor[nonemptyRoot](), reflect.TypeFor[nonemptyNode](), reflect.TypeFor[nonemptyMap]()}
		r := New(types, nil, nil)
		encode := r.Encode
		if framed {
			r = newFramedTestRegistry(types, nil, nil)
			encode = r.EncodeFramed
		}
		data, err := encode(source)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 8; i++ {
			value, err := r.Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			got := value.(*nonemptyRoot)
			if got.Values[0].(*nonemptyNode) != got.Node || got.Node.Next.(*nonemptyNode) != got.Node {
				t.Fatal("nonempty interface pointer identity or cycle changed")
			}
			got.Node.N = 9
			got.Map["n"] = 99
			if got.Values[0].Number() != 9 || got.Values[1].Number() != 99 || node.N != 7 || m["n"] != 2 {
				t.Fatal("interface aliases or independent graph ownership changed")
			}
			for j := 0; j < len(got.Values); j += 5 {
				if got.Values[j].(*nonemptyNode) != got.Node || got.Values[j+1].Number() != 99 {
					t.Fatal("cached interface stores changed an alias")
				}
				if got.Values[j+2] == nil || got.Values[j+2].(*nonemptyNode) != nil ||
					got.Values[j+3] == nil || got.Values[j+3].(nonemptyMap) != nil || got.Values[j+4] != nil {
					t.Fatal("typed nil or nil interface changed")
				}
			}
			runtime.KeepAlive(got)
		}
		corrupt := append([]byte(nil), data...)
		corrupt[len(corrupt)-1] ^= 1
		if value, err := r.Decode(corrupt); err == nil || value != nil {
			t.Fatal("checksum mismatch published a decoded interface graph")
		}
	}
}
