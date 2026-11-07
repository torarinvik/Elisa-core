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

func BenchmarkStoreFlowManyToMany(b *testing.B) {
	src := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	dst := []string{"i", "j", "k", "l", "m", "n", "o", "p"}
	c := &storeFlowCtx{scalars: map[string]bool{"j": true, "l": true, "n": true, "p": true}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.edges = c.edges[:0]
		c.flow(src, dst)
	}
}

func TestStoreFlowFilteredDestinationsMatchReference(t *testing.T) {
	for _, src := range [][]string{nil, {"a"}, {"a", "b", "s", "a"}} {
		for _, dst := range [][]string{nil, {"a"}, {"a", "s", "b", "c", "d", "e", "f", "g", "h", "i"}} {
			got := &storeFlowCtx{scalars: map[string]bool{"s": true, "c": true}}
			var want [][2]string
			for _, s := range src {
				if got.scalars[storeFlowBaseName(s)] {
					continue
				}
				for _, d := range dst {
					if got.scalars[storeFlowBaseName(d)] || s == d {
						continue
					}
					want = append(want, [2]string{s, d})
				}
			}
			got.flow(src, dst)
			if !reflect.DeepEqual(got.edges, want) {
				t.Fatalf("src %v dst %v: got %v want %v", src, dst, got.edges, want)
			}
		}
	}
}
