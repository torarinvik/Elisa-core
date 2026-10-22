package semantic

// A frame may share this backing array. Appending must allocate, and removing
// facts must use dropSMTFactsCopyOnWrite instead of compacting it in place.
func freezeSMTFactList(facts []smtFact) []smtFact {
	if len(facts) == 0 {
		return nil
	}
	return facts[:len(facts):len(facts)]
}

// Preserve the input until a fact actually needs to be removed. This lets
// branch entry frames and already captured sibling survivors remain immutable.
func dropSMTFactsCopyOnWrite(facts []smtFact, drop func(smtFact) bool) []smtFact {
	for i, fact := range facts {
		if !drop(fact) {
			continue
		}
		out := make([]smtFact, i, len(facts)-1)
		copy(out, facts[:i])
		for _, next := range facts[i+1:] {
			if !drop(next) {
				out = append(out, next)
			}
		}
		return out
	}
	return facts
}
