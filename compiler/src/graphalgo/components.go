package graphalgo

import "sort"

// Components returns dependency-first strongly connected components.
func Components(deps [][]int, sortMembers bool) [][]int {
	n := len(deps)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var comps [][]int
	var st []int
	next := 0
	type frame struct{ v, edge int }
	for root := 0; root < n; root++ {
		if index[root] >= 0 {
			continue
		}
		call := []frame{{root, 0}}
		index[root], low[root] = next, next
		next++
		st = append(st, root)
		onStack[root] = true
		for len(call) > 0 {
			f := &call[len(call)-1]
			if f.edge < len(deps[f.v]) {
				w := deps[f.v][f.edge]
				f.edge++
				if index[w] < 0 {
					index[w], low[w] = next, next
					next++
					st = append(st, w)
					onStack[w] = true
					call = append(call, frame{w, 0})
				} else if onStack[w] && index[w] < low[f.v] {
					low[f.v] = index[w]
				}
				continue
			}
			v := f.v
			call = call[:len(call)-1]
			if len(call) > 0 && low[v] < low[call[len(call)-1].v] {
				low[call[len(call)-1].v] = low[v]
			}
			if low[v] == index[v] {
				var comp []int
				for {
					w := st[len(st)-1]
					st = st[:len(st)-1]
					onStack[w] = false
					comp = append(comp, w)
					if w == v {
						break
					}
				}
				if sortMembers {
					sort.Ints(comp)
				}
				comps = append(comps, comp)
			}
		}
	}
	return comps
}
