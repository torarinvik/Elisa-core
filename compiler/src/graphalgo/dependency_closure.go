package graphalgo

// DependencyClosure returns unseen dependencies before their callers.
func DependencyClosure(deps [][]int, u int, have func(int) bool) []int {
	if len(deps[u]) == 0 {
		return nil
	}
	var out []int
	seen := map[int]bool{u: true}
	var visit func(int)
	visit = func(v int) {
		for _, d := range deps[v] {
			if !seen[d] {
				seen[d] = true
				if have(d) {
					continue
				}
				visit(d)
				out = append(out, d)
			}
		}
	}
	visit(u)
	return out
}
