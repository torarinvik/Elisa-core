package semantic

import "sort"

// CodegenFunctionPriorities returns recently re-analyzed function names as
// scheduling hints. This is not an exhaustive invalidation set and must never
// authorize reuse: every native unit still needs its current exact cache key.
func (r *Result) CodegenFunctionPriorities() []string {
	if r == nil || r.analyzer == nil || r.analyzer.retained == nil {
		return nil
	}
	retained := r.analyzer.retained
	if retained.LastStats.Changed == 0 {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, fn := range retained.rbChanged {
		if sym := r.analyzer.funcDeclSymbols[fn]; sym != nil && sym.Name != "" && !seen[sym.Name] {
			seen[sym.Name] = true
			names = append(names, sym.Name)
		}
	}
	sort.Strings(names)
	return names
}
