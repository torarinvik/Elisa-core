package graphalgo

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func recursiveComponents(deps [][]int) [][]int {
	index := make([]int, len(deps))
	low := make([]int, len(deps))
	onStack := make([]bool, len(deps))
	var stack []int
	var components [][]int
	next := 0
	var visit func(int)
	visit = func(v int) {
		next++
		index[v] = next
		low[v] = next
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range deps[v] {
			if index[w] == 0 {
				visit(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] != index[v] {
			return
		}
		var group []int
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			group = append(group, w)
			if w == v {
				break
			}
		}
		components = append(components, group)
	}
	for v := range deps {
		if index[v] == 0 {
			visit(v)
		}
	}
	return components
}

func TestComponentsMatchesOriginalTraversal(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	for trial := 0; trial < 1000; trial++ {
		n := random.Intn(14)
		deps := make([][]int, n)
		for i := range deps {
			for j := 0; j < n; j++ {
				if random.Intn(5) == 0 {
					deps[i] = append(deps[i], j)
				}
			}
		}
		want := recursiveComponents(deps)
		if got := Components(deps, false); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: got %v want %v", trial, got, want)
		}
		for _, group := range want {
			sort.Ints(group)
		}
		if got := Components(deps, true); !reflect.DeepEqual(got, want) {
			t.Fatalf("sorted trial %d: got %v want %v", trial, got, want)
		}
	}
}

func TestComponentsHandlesDeepGraph(t *testing.T) {
	const n = 100000
	deps := make([][]int, n)
	for i := 0; i+1 < n; i++ {
		deps[i] = []int{i + 1}
	}
	groups := Components(deps, false)
	if len(groups) != n || groups[0][0] != n-1 || groups[n-1][0] != 0 {
		t.Fatal("incorrect dependency ordering")
	}
}
