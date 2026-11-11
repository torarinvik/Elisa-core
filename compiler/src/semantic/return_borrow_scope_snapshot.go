package semantic

// snapshotReturnBorrowScope flattens the function-local scope levels into one scope over the global
// scope, so a name declared later in the same block cannot capture a deferred check's lookups.
func (a *Analyzer) snapshotReturnBorrowScope(frame *returnBorrowDeferFrame) (*Scope, bool) {
	var inlineLevels [8]*Scope
	levels := inlineLevels[:0]
	symbolCapacity := 0
	cur := a.currentScope
	for cur != nil && cur != a.globalScope {
		levels = append(levels, cur)
		if len(cur.Symbols) > symbolCapacity {
			symbolCapacity = len(cur.Symbols)
		}
		cur = cur.Parent
	}
	if cur != a.globalScope {
		return nil, false
	}
	if frame != nil && frame.matchesSnapshotScopes(levels, a.globalScope) {
		return deferredBorrowScope(a.globalScope, frame.snapshotSymbols), true
	}
	// The largest scope is a lower bound on the unique binding count.
	symbols := make(map[string]*Symbol, symbolCapacity)
	for _, level := range levels { // innermost first: the first definition wins
		for name, sym := range level.Symbols {
			if _, exists := symbols[name]; !exists {
				symbols[name] = sym
			}
		}
	}
	if frame != nil {
		frame.snapshotScopes = append(frame.snapshotScopes[:0], levels...)
		frame.snapshotVersions = frame.snapshotVersions[:0]
		for _, level := range levels {
			frame.snapshotVersions = append(frame.snapshotVersions, level.symbolsVersion)
		}
		frame.snapshotSymbols = symbols
		frame.snapshotGlobal = a.globalScope
	}
	return deferredBorrowScope(a.globalScope, symbols), true
}

func deferredBorrowScope(global *Scope, symbols map[string]*Symbol) *Scope {
	scope := &Scope{Parent: global, Symbols: symbols, symbolsVersion: uint64(len(symbols)), sharedSymbols: true}
	if global != nil {
		scope.returnBorrowFrame = global.returnBorrowFrame
	}
	return scope
}

func (frame *returnBorrowDeferFrame) matchesSnapshotScopes(levels []*Scope, global *Scope) bool {
	if frame.snapshotSymbols == nil || frame.snapshotGlobal != global || len(frame.snapshotScopes) != len(levels) {
		return false
	}
	for i, level := range levels {
		if frame.snapshotScopes[i] != level || frame.snapshotVersions[i] != level.symbolsVersion {
			return false
		}
	}
	return true
}
