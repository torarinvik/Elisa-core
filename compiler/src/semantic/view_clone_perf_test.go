package semantic

import (
	"fmt"
	"maps"
	"testing"
)

var benchmarkViewCloneSink map[string]int64

func TestViewStateCloneIndependence(t *testing.T) {
	for _, n := range []int{0, 8, 32} {
		lengths := make(map[string]int64, n)
		mutable := make(map[string]bool, n)
		for i := 0; i < n; i++ {
			key := fmt.Sprint(i)
			lengths[key] = int64(i)
			mutable[key] = true
		}
		lengthCopy := cloneViewStaticLen(lengths)
		mutableCopy := cloneViewMutable(mutable)
		if n == 0 {
			if lengthCopy != nil || mutableCopy != nil {
				t.Fatal("empty states should remain nil")
			}
			continue
		}
		lengthCopy["0"] = -1
		mutableCopy["0"] = false
		if lengths["0"] != 0 || !mutable["0"] {
			t.Fatalf("clone mutated original for %d entries", n)
		}
	}
}

func BenchmarkViewStateClone(b *testing.B) {
	for _, n := range []int{8, 32, 128} {
		src := make(map[string]int64, n)
		for i := 0; i < n; i++ {
			src[fmt.Sprint(i)] = int64(i)
		}
		b.Run(fmt.Sprintf("loop/%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				dst := make(map[string]int64, len(src))
				for k, v := range src {
					dst[k] = v
				}
				benchmarkViewCloneSink = dst
			}
		})
		b.Run(fmt.Sprintf("maps/%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				benchmarkViewCloneSink = maps.Clone(src)
			}
		})
	}
}
