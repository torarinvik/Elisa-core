package semantic

import "math"

// A program-lived payload proof (calleeStoresProgramLivedElements) assumes success on
// re-entering an open frame, so a result computed under such an assumption is only
// tentative and stays confined to its root proof. A result that consulted no frame
// still open above it, no open store-mode cycle and nothing of the body being
// analyzed is the same for every root query: it reads only callees whose analysis
// has settled. Those results are memoized until currentFuncDecl next changes; every
// assignment to it drops the cache, so no entry outlives the body that computed it.

const storeLivedNoAssumption = math.MaxInt

type storeLivedDone struct {
	result bool
	// tentative marks a result that depended on an assumption. Its frame may have
	// closed since, so a reuse is never final.
	tentative bool
}

type storeLivedProof struct {
	active    map[storeScanKey]int // open frame depth
	completed map[storeScanKey]storeLivedDone
	depth     int
	assumed   int // shallowest open frame consulted by the running frame's subtree
}

type storeLivedFrame struct {
	depth        int
	savedAssumed int
	savedCycle   bool
}

func newStoreLivedProof() *storeLivedProof {
	return &storeLivedProof{active: map[storeScanKey]int{}, completed: map[storeScanKey]storeLivedDone{}, assumed: storeLivedNoAssumption}
}

func (r *storeLivedProof) consult(depth int) {
	r.assumed = min(r.assumed, depth)
}

func (r *storeLivedProof) enter(a *Analyzer, key storeScanKey) storeLivedFrame {
	frame := storeLivedFrame{depth: r.depth, savedAssumed: r.assumed, savedCycle: a.storeScanCycleHit}
	r.active[key] = r.depth
	r.depth++
	r.assumed = storeLivedNoAssumption
	a.storeScanCycleHit = false
	if a.currentFuncDecl == nil || key.decl == a.readDecl(a.currentFuncDecl) {
		// The body under analysis is still gaining expression types.
		r.assumed = -1
	}
	return frame
}

// leave closes the frame and reports whether its result is final.
func (r *storeLivedProof) leave(a *Analyzer, frame storeLivedFrame, key storeScanKey, result bool) bool {
	delete(r.active, key)
	r.depth--
	assumed := r.assumed
	cycle := a.storeScanCycleHit
	if cycle {
		assumed = -1
	}
	if assumed >= frame.depth {
		// Only this frame's own re-entry was assumed; it is closed now.
		assumed = storeLivedNoAssumption
	}
	r.completed[key] = storeLivedDone{result: result, tentative: assumed != storeLivedNoAssumption}
	r.assumed = min(frame.savedAssumed, assumed)
	a.storeScanCycleHit = frame.savedCycle || cycle
	return assumed == storeLivedNoAssumption
}

func (a *Analyzer) storeLivedFinal(key storeScanKey) (bool, bool) {
	if storeScanTrace {
		return false, false
	}
	result, ok := a.storeLivedCache[key]
	return result, ok
}

func (a *Analyzer) storeLivedRecord(key storeScanKey, result bool) {
	if storeScanTrace || a.currentFuncDecl == nil {
		return
	}
	if a.storeLivedCache == nil {
		a.storeLivedCache = map[storeScanKey]bool{}
	}
	a.storeLivedCache[key] = result
}
