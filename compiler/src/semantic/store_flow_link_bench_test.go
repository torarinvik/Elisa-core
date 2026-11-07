package semantic

import (
	"reflect"
	"testing"
)

func BenchmarkStoreFlowLink(b *testing.B) {
	names := []string{"source", "target", "value", "other"}
	c := &storeFlowCtx{written: map[string]bool{"target": true}, scalars: map[string]bool{}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.edges = c.edges[:0]
		c.link(names)
	}
}

func TestStoreFlowLinkMatchesPartitionedUnions(t *testing.T) {
	tests := [][]string{{}, {"a"}, {"a", "b", "c", "d"}, {"s", "a", "a", "b", "s", "c", "d", "e", "f", "g", "h", "i"}, {"target", "other", "target", "source"}}
	for _, names := range tests {
		got := &storeFlowCtx{written: map[string]bool{"target": true, "b": true, "e": true}, scalars: map[string]bool{"s": true}}
		want := &storeFlowCtx{written: got.written, scalars: got.scalars}
		var sinks, sources []string
		for _, n := range names {
			if want.scalars[storeFlowBaseName(n)] {
				continue
			}
			if want.isPure(n) {
				sources = append(sources, n)
			} else {
				sinks = append(sinks, n)
			}
		}
		want.u.unionAll(sinks)
		want.pu.unionAll(sources)
		if len(sinks) > 0 {
			for _, src := range sources {
				want.edges = append(want.edges, [2]string{src, sinks[0]})
			}
		}
		got.link(names)
		if !reflect.DeepEqual(got.u.parent, want.u.parent) || !reflect.DeepEqual(got.pu.parent, want.pu.parent) || !reflect.DeepEqual(got.edges, want.edges) {
			t.Fatalf("names %v: got %#v, want %#v", names, got, want)
		}
	}
}
