package semantic

import (
	"container/heap"
)

// These callbacks exist only during one fresh analyzer's final effect inference.
// Reads include concrete declarations behind generic/function-value copies.
// Lambda row writes must also wake their readers even if the owning function's
// own execution row stays equal.
type permissionInferenceObserver struct {
	read    func(*FuncType)
	changed func(*FuncType)
}

func (a *Analyzer) observePermissionFunction(ft *FuncType) {
	if a.permissionInference != nil && a.permissionInference.read != nil && ft != nil {
		a.permissionInference.read(ft)
	}
}

type permissionFunctionQueue []int

func (q permissionFunctionQueue) Len() int           { return len(q) }
func (q permissionFunctionQueue) Less(i, j int) bool { return q[i] < q[j] }
func (q permissionFunctionQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *permissionFunctionQueue) Push(x any)        { *q = append(*q, x.(int)) }
func (q *permissionFunctionQueue) Pop() any {
	old := *q
	x := old[len(old)-1]
	*q = old[:len(old)-1]
	return x
}

// permissionInferenceOutcome distinguishes queue exhaustion from budget exhaustion.
// A partial row must never be admitted as a completed incremental callee summary.
type permissionInferenceOutcome struct {
	Evaluations, Rounds, Pending int
	Converged                    bool
}

func (a *Analyzer) inferFunctionPermissionEffectsWorklist(decls []scopedDecl) int {
	outcome := a.solveFunctionPermissionEffects(decls, len(decls)+4)
	a.permissionEffectsOutcome = outcome
	return outcome.Evaluations
}

// Dirty functions execute in declaration order. An earlier/self reader goes
// into the next round; a later reader can execute in the current round.
// maxRounds includes the initial dense pass. Tests use a smaller budget to
// exercise the incomplete path without changing the production scheduling cap.
func (a *Analyzer) solveFunctionPermissionEffects(decls []scopedDecl, maxRounds int) permissionInferenceOutcome {
	n := len(decls)
	outcome := permissionInferenceOutcome{Converged: n == 0}
	if n == 0 {
		return outcome
	}
	if maxRounds <= 0 {
		outcome.Pending = n
		return outcome
	}
	outcome.Rounds = 1
	// Most files already have stable body-local rows. Keep their common case
	// to one dense pass without allocating the graph or heaps. Observe nested
	// lambda writes too; they may change even when the owner's row stays equal.
	saved := a.permissionInference
	defer func() { a.permissionInference = saved }()
	changed := false
	a.permissionInference = &permissionInferenceObserver{changed: func(*FuncType) { changed = true }}
	for _, decl := range decls {
		_, different := a.recomputeFunctionPermissionEffects(decl)
		changed = changed || different
	}
	if !changed {
		outcome.Evaluations = n
		outcome.Converged = true
		return outcome
	}
	current := make(permissionFunctionQueue, n)
	currentQueued := make([]bool, n)
	next := permissionFunctionQueue{}
	nextQueued := make([]bool, n)
	for i := range current {
		current[i] = i
		currentQueued[i] = true
	}
	reads := make([]map[*FuncType]struct{}, n)
	readers := map[*FuncType]map[int]struct{}{}
	index := -1
	enqueue := func(i int) {
		if i > index {
			if !currentQueued[i] {
				heap.Push(&current, i)
				currentQueued[i] = true
			}
		} else if !nextQueued[i] {
			heap.Push(&next, i)
			nextQueued[i] = true
		}
	}
	a.permissionInference = &permissionInferenceObserver{
		read: func(ft *FuncType) {
			if reads[index] == nil {
				reads[index] = map[*FuncType]struct{}{}
			}
			if _, ok := reads[index][ft]; ok {
				return
			}
			reads[index][ft] = struct{}{}
			if readers[ft] == nil {
				readers[ft] = map[int]struct{}{}
			}
			readers[ft][index] = struct{}{}
		},
		changed: func(ft *FuncType) {
			for i := range readers[ft] {
				enqueue(i)
			}
		},
	}
	evaluations := n
	for round := 1; round < maxRounds && len(current) > 0; round++ {
		outcome.Rounds++
		for len(current) > 0 {
			index = heap.Pop(&current).(int)
			currentQueued[index] = false
			for ft := range reads[index] {
				delete(readers[ft], index)
				if len(readers[ft]) == 0 {
					delete(readers, ft)
				}
			}
			reads[index] = nil
			ft, changed := a.recomputeFunctionPermissionEffects(decls[index])
			evaluations++
			if changed {
				a.permissionInference.changed(ft)
			}
		}
		current, next = next, current
		currentQueued, nextQueued = nextQueued, currentQueued
	}
	outcome.Evaluations = evaluations
	outcome.Pending = len(current)
	outcome.Converged = len(current) == 0
	return outcome
}
