package semantic

import (
	"fmt"
	"maps"
	"math/rand"
	"reflect"
	"testing"
)

func referenceStoreFlowSpread(written, grown, params map[string]bool, groups, elements [][]string) {
	spread := func(set map[string]bool, gs [][]string) bool {
		changed := false
		for _, g := range gs {
			hit := false
			for _, n := range g {
				if set[n] {
					hit = true
					break
				}
			}
			if hit {
				for _, n := range g {
					if !set[n] {
						set[n] = true
						changed = true
					}
				}
			}
		}
		return changed
	}
	for changed := true; changed; {
		changed = spread(written, groups)
		if spread(grown, groups) {
			changed = true
		}
		for n := range grown {
			if params[n] && !written[n] && storeFlowInGroups(n, elements) {
				written[n] = true
				changed = true
			}
		}
		if spread(written, elements) {
			changed = true
		}
	}
	for n := range grown {
		written[n] = true
	}
}

func TestStoreFlowIndexedSpreadMatchesFixpoint(t *testing.T) {
	r := rand.New(rand.NewSource(115))
	for trial := 0; trial < 2000; trial++ {
		groups, elements := [][]string{}, [][]string{}
		for i := 0; i < r.Intn(80); i++ {
			g := []string{}
			for j := 0; j < r.Intn(5); j++ {
				g = append(g, fmt.Sprint(r.Intn(25)))
			}
			if r.Intn(2) == 0 {
				groups = append(groups, g)
			} else {
				elements = append(elements, g)
			}
		}
		written, grown, params := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for i := 0; i < 25; i++ {
			n := fmt.Sprint(i)
			if r.Intn(5) == 0 {
				written[n] = r.Intn(2) == 0
			}
			if r.Intn(5) == 0 {
				grown[n] = r.Intn(2) == 0
			}
			if r.Intn(5) == 0 {
				params[n] = r.Intn(2) == 0
			}
		}
		want, wantGrown := maps.Clone(written), maps.Clone(grown)
		referenceStoreFlowSpread(want, wantGrown, params, groups, elements)
		storeFlowPropagateWritesIndexed(written, grown, params, groups, elements)
		if !reflect.DeepEqual(written, want) || !reflect.DeepEqual(grown, wantGrown) {
			t.Fatalf("trial %d: got %v %v, want %v %v", trial, written, grown, want, wantGrown)
		}
	}
}

func BenchmarkStoreFlowLongSpread(b *testing.B) {
	groups := make([][]string, 256)
	for i := range groups {
		groups[i] = []string{fmt.Sprint(255 - i), fmt.Sprint(256 - i)}
	}
	for _, indexed := range []bool{false, true} {
		name := "fixpoint"
		if indexed {
			name = "indexed"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				w, g := map[string]bool{"0": true}, map[string]bool{}
				if indexed {
					storeFlowPropagateWritesIndexed(w, g, nil, groups, nil)
				} else {
					referenceStoreFlowSpread(w, g, nil, groups, nil)
				}
				if !w["256"] {
					b.Fatal("closure incomplete")
				}
			}
		})
	}
}
