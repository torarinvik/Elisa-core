package semantic

import (
	"container/heap"
	"sync"
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
	return a.solveFunctionPermissionEffectsWithReadLimit(decls, maxRounds, permissionInitialReadLimit)
}

const permissionInitialReadLimit = 1 << 20
const permissionInitialReadRetain = 1 << 16

type permissionInitialRead struct {
	target     *FuncType
	reader, at int
}

var permissionInitialReads = sync.Pool{New: func() any { return make([]permissionInitialRead, 0, 256) }}

func (a *Analyzer) solveFunctionPermissionEffectsWithReadLimit(decls []scopedDecl, maxRounds, readLimit int) permissionInferenceOutcome {

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
	// Keep the first pass's actual read/write ordering. A function only needs
	// another evaluation if a row it read changed later; fresh later readers
	// already consumed the new value. The bounded flat trace avoids graph maps
	// on stable files and is cleared before pooling so no old Type arena survives.
	saved := a.permissionInference
	defer func() { a.permissionInference = saved }()
	initial := permissionInitialReads.Get().([]permissionInitialRead)[:0]
	defer func() {
		if cap(initial) <= permissionInitialReadRetain {
			clear(initial)
			permissionInitialReads.Put(initial[:0])
		}
	}()
	writes := map[*FuncType]int(nil)
	clock, reader := 0, 0
	changed, fallback := false, false
	markChanged := func(ft *FuncType) {
		changed = true
		clock++
		if ft == nil {
			fallback = true
			return
		}
		if !fallback {
			if writes == nil {
				writes = map[*FuncType]int{}
			}
			writes[ft] = clock
		}
	}
	a.permissionInference = &permissionInferenceObserver{
		read: func(ft *FuncType) {
			if fallback {
				return
			}
			// Repeated adjacent reads by the two collectors need only the
			// earliest observation. Keeping it is conservative even if a write
			// intervened: an earlier consumed value still requires validation.
			if len(initial) > 0 {
				last := initial[len(initial)-1]
				if last.reader == reader && last.target == ft {
					return
				}
			}
			if len(initial) >= readLimit {
				fallback = true
				clear(initial)
				initial = initial[:0]
				return
			}
			clock++
			initial = append(initial, permissionInitialRead{ft, reader, clock})
		},
		changed: markChanged,
	}
	for i, decl := range decls {
		reader = i
		ft, different := a.recomputeFunctionPermissionEffects(decl)
		if different {
			markChanged(ft)
		}
	}
	outcome.Evaluations = n
	if !changed {
		outcome.Converged = true
		return outcome
	}
	current := permissionFunctionQueue{}
	currentQueued := make([]bool, n)
	if fallback {
		for i := 0; i < n; i++ {
			current = append(current, i)
			currentQueued[i] = true
		}
	} else {
		for _, read := range initial {
			if writes[read.target] > read.at {
				currentQueued[read.reader] = true
			}
		}
		for i, dirty := range currentQueued {
			if dirty {
				current = append(current, i)
			}
		}
	}
	if len(current) == 0 {
		outcome.Converged = true
		return outcome
	}
	next := permissionFunctionQueue{}
	nextQueued := make([]bool, n)
	reads := make([]map[*FuncType]struct{}, n)
	readers := map[*FuncType]map[int]struct{}{}
	if !fallback {
		for _, read := range initial {
			i, ft := read.reader, read.target
			if reads[i] == nil {
				reads[i] = map[*FuncType]struct{}{}
			}
			if _, present := reads[i][ft]; present {
				continue
			}
			reads[i][ft] = struct{}{}
			if readers[ft] == nil {
				readers[ft] = map[int]struct{}{}
			}
			readers[ft][i] = struct{}{}
		}
	}
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
