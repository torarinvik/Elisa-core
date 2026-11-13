package graphalgo

import "sort"

// WorkUnits splits dependency components into schedulable chunks while making
// every caller wait for every chunk of each dependency component.
func WorkUnits(deps [][]int, bodyWeights []int, components [][]int, chunk int) (units [][]int, unitOf, componentOf []int, componentUnits map[int][]int, unitDeps [][]int, weights []int) {
	for component, members := range components {
		for start := 0; start < len(members); start += chunk {
			units = append(units, members[start:min(start+chunk, len(members))])
			componentOf = append(componentOf, component)
		}
	}
	unitOf = make([]int, len(deps))
	componentUnits = make(map[int][]int)
	for unit, members := range units {
		for _, body := range members {
			unitOf[body] = unit
		}
		componentUnits[componentOf[unit]] = append(componentUnits[componentOf[unit]], unit)
	}
	unitDeps = make([][]int, len(units))
	weights = make([]int, len(units))
	seen := make([]int, len(units))
	for unit, members := range units {
		generation := unit + 1
		seen[unit] = generation
		for _, body := range members {
			weights[unit] += bodyWeights[body] + 50
			for _, dependency := range deps[body] {
				other := unitOf[dependency]
				if seen[other] == generation || componentOf[other] == componentOf[unit] {
					continue
				}
				for _, sibling := range componentUnits[componentOf[other]] {
					if seen[sibling] != generation {
						seen[sibling] = generation
						unitDeps[unit] = append(unitDeps[unit], sibling)
					}
				}
			}
		}
		sort.Ints(unitDeps[unit])
	}
	return
}
