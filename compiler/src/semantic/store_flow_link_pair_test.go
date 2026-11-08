package semantic

import (
	"reflect"
	"testing"
)

func TestStoreFlowLinkPairMatchesCombined(t *testing.T) {
	for _, pair := range [][2][]string{{nil, nil}, {{"a", "b"}, {"c", "d"}}, {{"s", "a", "a"}, {"b", "s", "c"}}, {{"a"}, {"a", "b"}}} {
		got := &storeFlowCtx{written: map[string]bool{"b": true, "d": true}, scalars: map[string]bool{"s": true}}
		want := &storeFlowCtx{written: got.written, scalars: got.scalars}
		got.linkPair(pair[0], pair[1])
		want.link(append(append([]string{}, pair[0]...), pair[1]...))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("link mismatch for %v: got %#v, want %#v", pair, got, want)
		}
	}
}
