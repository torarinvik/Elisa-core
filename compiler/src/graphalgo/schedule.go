package graphalgo

// ScheduleDependencies constructs reverse edges, indegrees and critical-path
// priorities. Units must be ordered with dependencies before their callers.
func ScheduleDependencies(deps [][]int, weights, scratch []int) ([][]int, []int, []int) {
	reverse := make([][]int, len(deps))
	pending := make([]int, len(deps))
	priority := make([]int, len(deps))
	counts := scratch[:len(deps)]
	clear(counts)
	edgeCount := 0
	for unit := range deps {
		pending[unit] = len(deps[unit])
		for _, dependency := range deps[unit] {
			counts[dependency]++
			edgeCount++
		}
	}
	backing := make([]int, edgeCount)
	offset := 0
	for unit, count := range counts {
		if count != 0 {
			reverse[unit] = backing[offset : offset : offset+count]
			offset += count
		}
	}
	for unit := range deps {
		for _, dependency := range deps[unit] {
			reverse[dependency] = append(reverse[dependency], unit)
		}
	}
	for unit := len(deps) - 1; unit >= 0; unit-- {
		best := 0
		for _, caller := range reverse[unit] {
			best = max(best, priority[caller])
		}
		priority[unit] = best + weights[unit]
	}
	return reverse, pending, priority
}
