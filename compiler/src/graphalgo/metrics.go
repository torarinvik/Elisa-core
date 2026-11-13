package graphalgo

// ComponentMetrics measures weight and the longest weighted dependency path.
// Components must be in dependency-first order.
func ComponentMetrics(deps [][]int, weights []int, components [][]int) (total, largest, largestWeight, critical int) {
	componentOf := make([]int, len(deps))
	for component, members := range components {
		for _, member := range members {
			componentOf[member] = component
		}
	}
	finish := make([]int, len(components))
	for component, members := range components {
		weight, start := 0, 0
		for _, member := range members {
			weight += weights[member]
			for _, dependency := range deps[member] {
				other := componentOf[dependency]
				if other != component && finish[other] > start {
					start = finish[other]
				}
			}
		}
		finish[component] = start + weight
		critical = max(critical, finish[component])
		total += weight
		if len(members) > largest {
			largest, largestWeight = len(members), weight
		}
	}
	return
}
