package semantic

// Each SCC contributes its entire node count to a path bound: a simple DFS
// path can visit no more nodes in it, plus at most one closing/seen edge.
// This is conservative for large SCCs but linear in the bounded graph size.
type capabilityDepthGraph struct {
	nodes int
	edges [][2]int
}

func (g *capabilityDepthGraph) withinDepth(limit int) bool {
	if g.nodes <= limit {
		return true
	}
	adjacency := make([][]int, g.nodes)
	for _, edge := range g.edges {
		adjacency[edge[0]] = append(adjacency[edge[0]], edge[1])
	}
	indexes := make([]int, g.nodes)
	low := make([]int, g.nodes)
	active := make([]bool, g.nodes)
	component := make([]int, g.nodes)
	var stack, weights []int
	next := 0
	var visit func(int)
	visit = func(node int) {
		next++
		indexes[node] = next
		low[node] = next
		stack = append(stack, node)
		active[node] = true
		for _, child := range adjacency[node] {
			if indexes[child] == 0 {
				visit(child)
				if low[child] < low[node] {
					low[node] = low[child]
				}
			} else if active[child] && indexes[child] < low[node] {
				low[node] = indexes[child]
			}
		}
		if low[node] == indexes[node] {
			weight := 0
			for {
				child := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				active[child] = false
				component[child] = len(weights)
				weight++
				if child == node {
					break
				}
			}
			weights = append(weights, weight)
		}
	}
	for node := range indexes {
		if indexes[node] == 0 {
			visit(node)
		}
	}
	// Tarjan emits children before parents. Every inter-component edge goes
	// from a later component to an earlier one, so no second recursive walk.
	longest := append([]int(nil), weights...)
	dag := make([][]int, len(weights))
	for _, edge := range g.edges {
		a, b := component[edge[0]], component[edge[1]]
		if a != b {
			dag[a] = append(dag[a], b)
		}
	}
	for owner := range weights {
		for _, child := range dag[owner] {
			if path := weights[owner] + longest[child]; path > longest[owner] {
				longest[owner] = path
			}
		}
		if longest[owner] > limit {
			return false
		}
	}
	return true
}
