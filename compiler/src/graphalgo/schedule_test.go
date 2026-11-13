package graphalgo

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestScheduleDependencies(t *testing.T) {
	reverse, pending, priority := ScheduleDependencies([][]int{nil, {0}, {0}, {1, 2}}, []int{1, 2, 3, 4}, []int{9, 9, 9, 9})
	if !reflect.DeepEqual(reverse, [][]int{{1, 2}, {3}, {3}, nil}) {
		t.Fatalf("reverse edges: %v", reverse)
	}
	if !reflect.DeepEqual(pending, []int{0, 1, 1, 2}) {
		t.Fatalf("pending: %v", pending)
	}
	if !reflect.DeepEqual(priority, []int{8, 6, 7, 4}) {
		t.Fatalf("priorities: %v", priority)
	}
}

func TestScheduleDependenciesMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for n := 0; n < 60; n++ {
		deps := make([][]int, n)
		weights := make([]int, n)
		for i := range deps {
			weights[i] = 1 + rng.Intn(20)
			for d := 0; d < i; d++ {
				if rng.Intn(4) == 0 {
					deps[i] = append(deps[i], d)
				}
			}
		}
		expectedReverse := make([][]int, n)
		expectedPending := make([]int, n)
		expectedPriority := make([]int, n)
		for i := range deps {
			expectedPending[i] = len(deps[i])
			for _, d := range deps[i] {
				expectedReverse[d] = append(expectedReverse[d], i)
			}
		}
		for i := n - 1; i >= 0; i-- {
			expectedPriority[i] = weights[i]
			for _, caller := range expectedReverse[i] {
				expectedPriority[i] = max(expectedPriority[i], weights[i]+expectedPriority[caller])
			}
		}
		reverse, pending, priority := ScheduleDependencies(deps, weights, make([]int, n))
		if !reflect.DeepEqual(reverse, expectedReverse) || !reflect.DeepEqual(pending, expectedPending) || !reflect.DeepEqual(priority, expectedPriority) {
			t.Fatalf("mismatch for %d units", n)
		}
	}
}
