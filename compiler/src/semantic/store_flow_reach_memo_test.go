package semantic

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestStoreFlowReachMemoMatchesSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 100; trial++ {
		ctx := &storeFlowCtx{written: map[string]bool{}}
		names := make([]string, 12)
		for i := range names {
			names[i] = fmt.Sprint("n", i)
			ctx.written[names[i]] = rng.Intn(2) == 0
		}
		anchors := map[storeFlowClass][]storeFlowClass{}
		for i := 0; i < 35; i++ {
			src := ctx.storeFlowKey(names[rng.Intn(len(names))])
			dst := ctx.storeFlowKey(names[rng.Intn(len(names))])
			anchors[src] = append(anchors[src], dst)
		}
		memo := storeFlowReachMemo{ctx: ctx, anchors: anchors}
		for _, src := range names {
			for _, dst := range names {
				if got, want := memo.reaches(src, dst), ctx.reaches(anchors, src, dst); got != want {
					t.Fatalf("trial %d %s -> %s: got %v want %v", trial, src, dst, got, want)
				}
			}
		}
	}
}

func BenchmarkStoreFlowReachMatrix(b *testing.B) {
	ctx := &storeFlowCtx{written: map[string]bool{}}
	names := make([]string, 32)
	for i := range names {
		names[i] = fmt.Sprint("n", i)
		ctx.written[names[i]] = true
	}
	anchors := map[storeFlowClass][]storeFlowClass{}
	for i := 0; i < len(names)-1; i++ {
		src := ctx.storeFlowKey(names[i])
		anchors[src] = append(anchors[src], ctx.storeFlowKey(names[i+1]))
	}
	for _, memoized := range []bool{false, true} {
		label := "search"
		if memoized {
			label = "memo"
		}
		b.Run(label, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				memo := storeFlowReachMemo{ctx: ctx, anchors: anchors}
				for _, src := range names {
					for _, dst := range names {
						var got bool
						if memoized {
							got = memo.reaches(src, dst)
						} else {
							got = ctx.reaches(anchors, src, dst)
						}
						if src == dst && !got {
							b.Fatal("missing reflexive edge")
						}
					}
				}
			}
		})
	}
}
