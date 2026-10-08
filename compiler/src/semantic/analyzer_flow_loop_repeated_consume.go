package semantic

import (
	"sort"

	"elisacore/src/lexer"
)

// loopAffineFrame collects the affine state at each `continue` of one loop
// body. A `continue` ends the iteration just like falling off the body, so the
// back edge of the loop is the fall-through state joined with these.
//
// depth is the loopDepth INSIDE the loop body: a `continue` only belongs to the
// innermost loop, and loops that do not push a frame (parallel for, loop
// expressions) still bump loopDepth, so a `continue` in one of those never
// reaches an outer frame.
//
// broken collects the states at each `break`: they skip the back edge but reach
// the code after the loop.
type loopAffineFrame struct {
	depth     int
	outer     *Scope
	continued map[affineValueKey]affineValueState
	broken    map[affineValueKey]affineValueState
	// storageContinued joins the storage-view states at each `continue` (the back edge).
	storageContinued map[*Symbol]storageViewDependencyState
	// Jump edges carry current value types as well as ownership states. In
	// particular, a break skips the body's fall-through snapshot entirely.
	functionContinued      map[*Symbol]*FuncType
	functionBroken         map[*Symbol]*FuncType
	specializedContinued   map[*Symbol]Type
	specializedBroken      map[*Symbol]Type
	hasSpecializedContinue bool
	hasSpecializedBreak    bool
}

func (a *Analyzer) pushLoopAffineFrame() {
	a.loopAffineFrames = append(a.loopAffineFrames, loopAffineFrame{depth: a.loopDepth + 1, outer: a.currentScope})
}

func (a *Analyzer) popLoopAffineFrame() loopAffineFrame {
	n := len(a.loopAffineFrames)
	if n == 0 {
		return loopAffineFrame{}
	}
	frame := a.loopAffineFrames[n-1]
	a.loopAffineFrames = a.loopAffineFrames[:n-1]
	return frame
}

// noteLoopJumpAffineState records the state flowing along a `continue`
// (isBreak false) or a `break` (isBreak true).
func (a *Analyzer) noteLoopJumpAffineState(isBreak bool) {
	n := len(a.loopAffineFrames)
	if n == 0 || a.loopAffineFrames[n-1].depth != a.loopDepth {
		return
	}
	frame := &a.loopAffineFrames[n-1]
	// Keep the edges separate: only continue participates in a future cyclic
	// loop-head transfer. Both edges conservatively contribute to loop exit.
	functions := &frame.functionContinued
	types, seenTypes := &frame.specializedContinued, &frame.hasSpecializedContinue
	if isBreak {
		functions = &frame.functionBroken
		types, seenTypes = &frame.specializedBroken, &frame.hasSpecializedBreak
	}
	if !*seenTypes {
		*functions = a.cloneFunctionValueBindings()
		*types = a.cloneSpecializedValueTypeBindings()
		*seenTypes = true
	} else {
		*functions = a.mergeFunctionValueBindings(*functions, a.currentFunctionValues)
		*types = a.mergeSpecializedValueTypeBindings(*types, a.currentSpecializedValueTypes)
	}
	state := a.cloneAffineValueStates()
	if state == nil {
		state = map[affineValueKey]affineValueState{}
	}
	if isBreak {
		frame.broken = mergeAffineValueStates(frame.broken, state)
	} else {
		frame.continued = mergeAffineValueStates(frame.continued, state)
		a.noteLoopJumpStorageViewState(frame)
	}
}

// mergeLoopJumpSpecializedTypes must run before finishLoopAffineFrame pops the
// frame. Keep the zero-iteration predecessor and all explicit jump exits.
func (a *Analyzer) mergeLoopJumpSpecializedTypes(entry map[*Symbol]Type) map[*Symbol]Type {
	n := len(a.loopAffineFrames)
	if n == 0 || a.loopAffineFrames[n-1].depth != a.loopDepth+1 {
		return entry
	}
	frame := &a.loopAffineFrames[n-1]
	if frame.hasSpecializedContinue {
		entry = a.mergeSpecializedValueTypeBindings(entry, frame.specializedContinued)
	}
	if frame.hasSpecializedBreak {
		entry = a.mergeSpecializedValueTypeBindings(entry, frame.specializedBroken)
	}
	return entry
}

// checkLoopRepeatedConsume rejects a value that is live when the loop is
// entered, declared outside the loop, and consumed on the back edge: the second
// iteration would consume it again (a double move / double drop). Every
// iteration must either reinitialize it before the iteration ends or leave the
// loop after the move (`break` / `return`).
//
// entry is the state before the first iteration, backEdge the state at the end
// of an iteration, and outer the scope enclosing the loop (a root declared in
// the body is fresh each iteration and never reaches here through outer).
func (a *Analyzer) checkLoopRepeatedConsume(entry, backEdge map[affineValueKey]affineValueState, outer *Scope, pos lexer.Pos) {
	if len(backEdge) == 0 || outer == nil {
		return
	}
	var roots []*Symbol
	seen := map[*Symbol]bool{}
	for key, state := range backEdge {
		root := key.Root
		if root == nil || seen[root] || state.ConsumedBy == "" || state.ScheduledForDefer {
			continue
		}
		if a.loopRepeatReported[root] {
			continue
		}
		if found, ok := outer.Lookup(root.Name); !ok || found != root {
			continue
		}
		if !affineKeyLiveAtEntry(entry, key) {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].Name < roots[j].Name })
	for _, root := range roots {
		if a.loopRepeatReported == nil {
			a.loopRepeatReported = map[*Symbol]bool{}
		}
		a.loopRepeatReported[root] = true
		a.errorf(pos, "linear handle value %q declared outside the loop is consumed on every iteration; reinitialize it before the iteration ends or leave the loop after the move", root.Name)
	}
}

// affineKeyLiveAtEntry: no overlapping path of the same root is consumed,
// uninitialized or scheduled for defer at loop entry. A value already consumed
// on entry is a use-after-move that the body's own checks report.
func affineKeyLiveAtEntry(entry map[affineValueKey]affineValueState, key affineValueKey) bool {
	for ek, es := range entry {
		if ek.Root != key.Root {
			continue
		}
		if !affinePathContains(ek.Path, key.Path) && !affinePathContains(key.Path, ek.Path) {
			continue
		}
		if es.ConsumedBy != "" || es.Uninitialized || es.ScheduledForDefer {
			return false
		}
	}
	return true
}

// finishLoopAffineFrame pops the loop's frame, checks the back edge (the body's
// fall-through state, unless the body always leaves, joined with every
// `continue` state) and returns the `continue` and `break` states joined: both
// reach the code after the loop, which must see a value consumed on any of them
// as consumed.
func (a *Analyzer) finishLoopAffineFrame(entry, bodyAffine map[affineValueKey]affineValueState, bodyExits bool, outer *Scope, pos lexer.Pos) map[affineValueKey]affineValueState {
	frame := a.popLoopAffineFrame()
	backEdge := mergeAffineValueStates(nil, frame.continued)
	if !bodyExits {
		backEdge = mergeAffineValueStates(backEdge, bodyAffine)
	}
	a.checkLoopRepeatedConsume(entry, backEdge, outer, pos)
	return affineStatesVisibleFrom(mergeAffineValueStates(frame.continued, frame.broken), outer)
}

// affineStatesVisibleFrom keeps the keys whose root is declared in outer or an
// enclosing scope. A jump state still holds the body's own locals (the block
// that owns them never reached its exit on that path); letting them into the
// post-loop state would leave them dangling there as unconsumed.
func affineStatesVisibleFrom(states map[affineValueKey]affineValueState, outer *Scope) map[affineValueKey]affineValueState {
	if len(states) == 0 || outer == nil {
		return nil
	}
	kept := make(map[affineValueKey]affineValueState, len(states))
	for key, state := range states {
		if key.Root == nil {
			continue
		}
		if found, ok := outer.Lookup(key.Root.Name); ok && found == key.Root {
			kept[key] = state
		}
	}
	return kept
}

// reportLoopBodyLeaksOnJump rejects a `break` or `continue` that leaves the loop
// body while a must-consume value declared in the body is still live: the jump
// ends that value's scope on this path (a per-iteration linear local released
// only on the fall-through path leaks on the early one). Values declared
// outside the loop stay in scope and are checked where their own scope ends.
func (a *Analyzer) reportLoopBodyLeaksOnJump() {
	n := len(a.loopAffineFrames)
	if n == 0 || a.loopAffineFrames[n-1].depth != a.loopDepth || a.loopAffineFrames[n-1].outer == nil {
		return
	}
	outer := a.loopAffineFrames[n-1].outer
	a.reportUnconsumedProtocolValuesWhere(func(root *Symbol) bool {
		sym, ok := outer.Lookup(root.Name)
		return !ok || sym != root
	})
}

// Explicit break/continue edges must retain callable authority too; they can
// skip the body fall-through snapshot used by ordinary function-value merging.
func (a *Analyzer) mergeLoopJumpFunctionValues(entry map[*Symbol]*FuncType) map[*Symbol]*FuncType {
	n := len(a.loopAffineFrames)
	if n == 0 || a.loopAffineFrames[n-1].depth != a.loopDepth+1 {
		return entry
	}
	frame := &a.loopAffineFrames[n-1]
	if frame.hasSpecializedContinue {
		entry = a.mergeFunctionValueBindings(entry, frame.functionContinued)
	}
	if frame.hasSpecializedBreak {
		entry = a.mergeFunctionValueBindings(entry, frame.functionBroken)
	}
	return entry
}
