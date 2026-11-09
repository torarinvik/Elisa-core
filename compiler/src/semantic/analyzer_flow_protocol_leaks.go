package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"sort"
	"sync"
)

func (a *Analyzer) containsProtocolLeakValues(t Type) bool {
	hasThread, hasTask, hasGuard := a.protocolKindsInType(t, map[string]bool{})
	return hasThread || hasTask || hasGuard
}

func (a *Analyzer) protocolLeakDescription(t Type) string {
	if kind := directProtocolLeakKind(t); kind != "" {
		return kind
	}
	// A darray of affine elements must be drained before scope exit.
	if tt, ok := t.(*DArrayType); ok && a.containsAffineHandleValues(tt.Elem, map[string]bool{}) {
		return "darray of must-consume elements (drain with `for x in move c:`)"
	}
	hasThread, hasTask, hasGuard := a.protocolKindsInType(t, map[string]bool{})
	parts := make([]string, 0, 3)
	if hasThread {
		parts = append(parts, "joinable thread handles")
	}
	if hasTask {
		parts = append(parts, "pending task handles")
	}
	if hasGuard {
		parts = append(parts, "held mutex guards")
	}
	if len(parts) == 0 {
		return "linear value"
	}
	return "value containing " + joinProtocolLeakKinds(parts)
}

// Builtin scalar types cannot carry protocol state. In this caller the seen
// map is a disposable copy, so they need no cycle-tracking work.
func (a *Analyzer) protocolFieldContainsCarrier(t Type, seen map[string]bool) bool {
	if _, scalar := t.(*BuiltinType); scalar {
		return false
	}
	if seen == nil {
		return a.containsTrackedProtocolCarrierValues(t, nil)
	}
	copySeen := protocolFieldSeenPool.Get().(map[string]bool)
	for key, value := range seen {
		copySeen[key] = value
	}
	result := a.containsTrackedProtocolCarrierValues(t, copySeen)
	if len(copySeen) <= 8192 {
		clear(copySeen)
		protocolFieldSeenPool.Put(copySeen)
	}
	return result
}

var protocolFieldSeenPool = sync.Pool{New: func() any { return make(map[string]bool) }}

func (a *Analyzer) protocolLiveLeafPaths(t Type, prefix string, seen map[string]bool) map[string]Type {
	if t == nil {
		return nil
	}
	if kind := directProtocolLeakKind(t); kind != "" {
		return map[string]Type{prefix: t}
	}
	if kind := terminalTypestateLeakKind(t); kind != "" {
		return map[string]Type{prefix: t}
	}
	key := t.String()
	if seen[key] {
		return nil
	}
	seen[key] = true
	switch tt := t.(type) {
	case *StructType:
		paths := map[string]Type{}
		for _, field := range tt.Fields {
			if !a.protocolFieldContainsCarrier(field.Type, seen) {
				continue
			}
			for childPath, liveType := range a.protocolLiveLeafPaths(field.Type, field.Name, mapsCloneBool(seen)) {
				paths[joinAffinePath(prefix, childPath)] = liveType
			}
		}
		return paths
	case *EnumType:
		paths := map[string]Type{}
		for _, field := range tt.Common {
			if !a.protocolFieldContainsCarrier(field.Type, seen) {
				continue
			}
			for childPath, liveType := range a.protocolLiveLeafPaths(field.Type, field.Name, mapsCloneBool(seen)) {
				paths[joinAffinePath(prefix, childPath)] = liveType
			}
		}
		for _, variant := range tt.Variants {
			for i, payloadType := range variant.Payload {
				label := variant.PayloadLabel(i)
				if label == "" || !a.protocolFieldContainsCarrier(payloadType, seen) {
					continue
				}
				for childPath, liveType := range a.protocolLiveLeafPaths(payloadType, label, mapsCloneBool(seen)) {
					paths[joinAffinePath(prefix, childPath)] = liveType
				}
			}
		}
		if len(paths) != 0 {
			return paths
		}
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			paths := map[string]Type{}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				if !a.protocolFieldContainsCarrier(fieldType, seen) {
					continue
				}
				for childPath, liveType := range a.protocolLiveLeafPaths(fieldType, field.Name, mapsCloneBool(seen)) {
					paths[joinAffinePath(prefix, childPath)] = liveType
				}
			}
			return paths
		}
	// A darray whose elements contain must-consume values is itself must-drain:
	// dropping it at scope exit without a `for ... in move` drain would silently
	// leak all contained affine handles. Treat the container as an opaque leaf —
	// individual elements cannot be named statically. The drain (`for g in move c:`)
	// consumes the container symbol via recordAffineConsumption, which clears the
	// leaf entry and satisfies the scope-exit check.
	// Note: dict/set drain is not yet implemented, so they are not enforced here.
	case *DArrayType:
		if a.containsAffineHandleValues(tt.Elem, map[string]bool{}) {
			return map[string]Type{prefix: t}
		}
	}
	if a.containsProtocolLeakValues(t) {
		return map[string]Type{prefix: t}
	}
	return nil
}

func (a *Analyzer) recordAffineDestructureConsumption(expr ast.Expr, actual Type, reason string) {
	if expr == nil || actual == nil {
		return
	}
	if !a.containsAffineHandleValues(actual, map[string]bool{}) {
		return
	}
	key, ok := a.lookupAffineValueKey(expr)
	if !ok {
		return
	}
	a.recordAffineConsumption(key, reason)
}

func mapsCloneBool(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	cloned := make(map[string]bool, len(src))
	for key, value := range src {
		cloned[key] = value
	}
	return cloned
}

func (a *Analyzer) reportUnconsumedProtocolValues() {
	a.reportUnconsumedProtocolValuesWhere(nil)
}

// reportUnconsumedProtocolValuesOnExit is the check at an early function exit
// (`return`, a propagating `try`): every live must-consume value leaks there.
func (a *Analyzer) reportUnconsumedProtocolValuesOnExit() {
	a.reportUnconsumedProtocolValuesWhere(func(*Symbol) bool { return true })
}

// A nursery's parser-generated group is joined by the backend's scoped cleanup
// before an early exit leaves the nursery. The trailing wait in the desugared
// body handles ordinary fallthrough, but it is unreachable after return or a
// loop jump. Match the generated declaration in an active nursery so a user
// TaskGroup in the same body still has to be consumed explicitly.
func (a *Analyzer) nurseryGroupJoinedOnEarlyExit(sym *Symbol) bool {
	if sym == nil {
		return false
	}
	decl, ok := sym.Node.(*ast.VarDeclStmt)
	if !ok {
		return false
	}
	for _, pool := range a.currentPoolScopes {
		if pool.Node == nil || !pool.Node.Nursery || len(pool.Node.Body) < 2 {
			continue
		}
		if generated, ok := pool.Node.Body[0].(*ast.VarDeclStmt); ok && generated == decl {
			return true
		}
	}
	return false
}

// reportUnconsumedProtocolValuesWhere reports the live must-consume values of
// the current affine state whose root satisfies leaving (every root when nil).
// Each root is reported once per analysis: an early exit and the function end
// can both see the same leak.
func (a *Analyzer) reportUnconsumedProtocolValuesWhere(leaving func(*Symbol) bool) {
	if a.currentAffineValues == nil {
		return
	}
	keys := make([]affineValueKey, 0, len(a.currentAffineValues))
	for key := range a.currentAffineValues {
		if key.Root != nil {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Root.Name != keys[j].Root.Name {
			return keys[i].Root.Name < keys[j].Root.Name
		}
		return keys[i].Path < keys[j].Path
	})
	for _, key := range keys {
		state := a.currentAffineValues[key]
		if key.Root == nil || (state.LiveProtocolType == nil && state.LiveProtocolDescription == "") {
			continue
		}
		if leaving != nil && a.nurseryGroupJoinedOnEarlyExit(key.Root) {
			continue
		}
		// Region owners are discharged by the region machinery at the end of
		// their scope (synthesized `__auto_*` regions included), not by user
		// code on the exit path, so only the function-end check (leaving nil)
		// applies to them. A `lock m as g:` guard is likewise released by the
		// lock block on every exit from it.
		if leaving != nil && (key.Root.Kind == SymbolRegion || !leaving(key.Root)) {
			continue
		}
		if _, lockGuard := key.Root.Node.(*ast.LockStmt); leaving != nil && lockGuard {
			continue
		}
		if a.protocolLeakReported[key] {
			continue
		}
		if key.Root.Kind == SymbolRegion && key.Path == "" {
			if regionState, ok := a.currentRegions[key.Root]; ok && regionState.Allocated && !regionState.Destroyed {
				continue
			}
		}
		if a.protocolLeakReported == nil {
			a.protocolLeakReported = map[affineValueKey]bool{}
		}
		a.protocolLeakReported[key] = true
		pos := lexer.Pos{}
		if key.Root.Node != nil {
			pos = key.Root.Node.Pos()
		}
		description := state.LiveProtocolDescription
		if description == "" {
			if kind := terminalTypestateLeakKind(state.LiveProtocolType); kind != "" {
				description = kind
			} else {
				description = a.protocolLeakDescription(state.LiveProtocolType)
			}
		}
		a.errorf(pos, "%s %q must be consumed before scope exit", description, affineValueDisplayNameFromKey(key))
	}
}

func (a *Analyzer) refreshTerminalTypestateTracking(sym *Symbol, tracked Type) {
	if a == nil || sym == nil || a.currentAffineValues == nil {
		return
	}
	// A reference parameter is a borrowed view of the caller's typestate value.
	// Its terminal-state obligation belongs to the caller, so the callee must
	// not report its receiver (`self`) as leaked at scope exit. By-value
	// parameters and locals remain owned and continue through this tracker.
	if sym.Kind == SymbolParam {
		if _, borrowed := sym.Type.(*RefType); borrowed {
			return
		}
	}
	key := affineValueKey{Root: sym}
	state := a.currentAffineValues[key]
	// A terminal state is represented by the ABSENCE of a leak kind, so checking
	// only terminalTypestateLeakKind(tracked) cannot clear a previously recorded
	// non-terminal state when a transition returns to (for example) Closed.
	// Clear both sides of the transition explicitly, then re-register only a
	// currently non-terminal typestate.
	if terminalTypestateLeakKind(state.LiveProtocolType) != "" || isTerminalTypestate(state.LiveProtocolType) || isTerminalTypestate(tracked) {
		state.LiveProtocolType = nil
		state.LiveProtocolDescription = ""
		a.currentAffineValues[key] = state
	}
	if terminalTypestateLeakKind(tracked) != "" {
		state.LiveProtocolType = tracked
		state.LiveProtocolDescription = ""
		state.ConsumedBy = ""
		a.currentAffineValues[key] = state
	}
}

func affineValueDisplayNameFromKey(key affineValueKey) string {
	if key.Root == nil {
		return "<value>"
	}
	if key.Path == "" {
		return key.Root.Name
	}
	return key.Root.Name + "." + key.Path
}
