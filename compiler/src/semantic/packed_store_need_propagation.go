package semantic

// Propagate requirements from callees that have facts. A caller whose facts
// grow is revisited, including when it belongs to a recursive call cycle.
func propagatePackedStoreNeeds(order []*FuncType, callees map[*FuncType]map[*FuncType]bool, needs map[*FuncType]map[string]*EnumType, builds map[*FuncType]map[string]string) {
	callers := make(map[*FuncType][]*FuncType)
	var pending []*FuncType
	queued := make(map[*FuncType]bool)
	for _, fn := range order {
		for callee := range callees[fn] {
			if callee != fn {
				callers[callee] = append(callers[callee], fn)
			}
		}
		if len(needs[fn]) != 0 || len(builds[fn]) != 0 {
			pending = append(pending, fn)
			queued[fn] = true
		}
	}
	for len(pending) != 0 {
		last := len(pending) - 1
		callee := pending[last]
		pending = pending[:last]
		delete(queued, callee)
		for _, caller := range callers[callee] {
			changed := false
			for name, root := range needs[callee] {
				if needs[caller][name] == nil {
					if needs[caller] == nil {
						needs[caller] = make(map[string]*EnumType)
					}
					needs[caller][name] = root
					changed = true
				}
			}
			for name, witness := range builds[callee] {
				if _, known := builds[caller][name]; !known {
					if builds[caller] == nil {
						builds[caller] = make(map[string]string)
					}
					builds[caller][name] = witness
					changed = true
				}
			}
			if changed && !queued[caller] {
				pending = append(pending, caller)
				queued[caller] = true
			}
		}
	}
}
