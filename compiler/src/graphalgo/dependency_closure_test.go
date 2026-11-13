package graphalgo

import (
	"reflect"
	"testing"
)

func TestDependencyClosureOrderAndPruning(t *testing.T) {
	deps := [][]int{nil, {0}, {0}, {1, 2}}
	if got := DependencyClosure(deps, 3, func(int) bool { return false }); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("closure: %v", got)
	}
	if got := DependencyClosure(deps, 3, func(i int) bool { return i == 1 }); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("pruned closure: %v", got)
	}
	if got := DependencyClosure(deps, 0, func(int) bool { return false }); got != nil {
		t.Fatalf("empty closure: %v", got)
	}
}
