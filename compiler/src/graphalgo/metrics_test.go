package graphalgo

import "testing"

func TestComponentMetrics(t *testing.T) {
	deps := [][]int{{}, {0}, {0}, {1, 2}}
	weights := []int{2, 3, 5, 7}
	components := [][]int{{0}, {1}, {2}, {3}}
	total, largest, largestWeight, critical := ComponentMetrics(deps, weights, components)
	if total != 17 || largest != 1 || largestWeight != 2 || critical != 14 {
		t.Fatalf("got total=%d largest=%d weight=%d critical=%d", total, largest, largestWeight, critical)
	}
}
