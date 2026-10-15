package semantic

import (
	"fmt"
	"os"

	"elisacore/src/ast"
)

// Store-scan DFS frames. calleeStoreModeIntoSeen / calleeReturnModeOf search callee summaries
// depth first; a key already on the search STACK is a recursive cycle and answers `none` (a
// cycle adds no store the rest of the body does not already show, which makes the cycle head's
// own result its least fixpoint). A result is final -- cacheable for good -- when its search
// consulted no frame strictly above it on the stack. Otherwise it is provisional: valid only
// while the deepest such frame is still open, and it may be reused only then. Reading a
// finished-but-provisional key as `none` (a visited set instead of a stack) dropped the stores
// of a sibling path: query order then decided the verdict.

type storeScanFrame struct {
	key storeScanKey
	id  uint64
	// dep: the deepest frame strictly above this one that its search consulted (-1: none).
	dep int
}

type storeScanProvisional struct {
	mode  storeMode
	depth int
	id    uint64
}

type storeScanFrameMark struct {
	depth   int
	prev    int
	hadPrev bool
}

// storeScanNoteCycle records that the running search consulted the open frame at depth.
func (a *Analyzer) storeScanNoteCycle(depth int) {
	a.storeScanCycleHit = true
	if n := len(a.storeScanFrames); n > 0 {
		top := &a.storeScanFrames[n-1]
		if depth < n-1 && depth > top.dep {
			top.dep = depth
		}
	}
}

// storeScanOpenCycle answers a key that is on the search stack.
func (a *Analyzer) storeScanOpenCycle(key storeScanKey) {
	depth, ok := a.storeScanFrameAt[key]
	if !ok {
		depth = 0 // unknown depth: depend on the outermost frame (never final early)
	}
	a.storeScanNoteCycle(depth)
}

// storeScanReuseProvisional returns a provisional result whose open dependencies still stand.
func (a *Analyzer) storeScanReuseProvisional(key storeScanKey) (storeMode, bool) {
	p, ok := a.storeScanProvisionalMemo[key]
	if !ok || p.depth >= len(a.storeScanFrames) || a.storeScanFrames[p.depth].id != p.id {
		return storeNone, false
	}
	a.storeScanNoteCycle(p.depth)
	return p.mode, true
}

func (a *Analyzer) storeScanPushFrame(key storeScanKey) storeScanFrameMark {
	if a.storeScanFrameAt == nil {
		a.storeScanFrameAt = map[storeScanKey]int{}
	}
	a.storeScanFrameSeq++
	mark := storeScanFrameMark{depth: len(a.storeScanFrames)}
	mark.prev, mark.hadPrev = a.storeScanFrameAt[key]
	a.storeScanFrameAt[key] = mark.depth
	a.storeScanFrames = append(a.storeScanFrames, storeScanFrame{key: key, id: a.storeScanFrameSeq, dep: -1})
	return mark
}

// storeScanPopFrame closes the frame and reports whether its result is final. A provisional
// result is remembered for reuse while its dependency stays open.
func (a *Analyzer) storeScanPopFrame(key storeScanKey, mark storeScanFrameMark, mode storeMode) bool {
	frame := a.storeScanFrames[mark.depth]
	a.storeScanFrames = a.storeScanFrames[:mark.depth]
	if mark.hadPrev {
		a.storeScanFrameAt[key] = mark.prev
	} else {
		delete(a.storeScanFrameAt, key)
	}
	if mark.depth > 0 {
		parent := &a.storeScanFrames[mark.depth-1]
		if frame.dep >= 0 && frame.dep < mark.depth-1 && frame.dep > parent.dep {
			parent.dep = frame.dep
		}
	}
	if frame.dep < 0 {
		delete(a.storeScanProvisionalMemo, key)
		return true
	}
	if a.storeScanProvisionalMemo == nil {
		a.storeScanProvisionalMemo = map[storeScanKey]storeScanProvisional{}
	}
	a.storeScanProvisionalMemo[key] = storeScanProvisional{mode: mode, depth: frame.dep, id: a.storeScanFrames[frame.dep].id}
	return false
}

// storeScanBeginSearch starts an independent top-level search (a fresh `seen`): frames of an
// enclosing search are not cycles of this one.
func (a *Analyzer) storeScanBeginSearch() func() {
	frames, at := a.storeScanFrames, a.storeScanFrameAt
	a.storeScanFrames, a.storeScanFrameAt = nil, nil
	return func() { a.storeScanFrames, a.storeScanFrameAt = frames, at }
}

// storeScanSettled runs a summary query for a filled-local check. Filled is a greatest fixpoint:
// a local stays filled only while every write is proven to carry at most views. An answer that
// still depends on an open frame used that frame's provisional `none` -- optimistic, and it must
// never prove a local filled (that would lower the very mode the open frame is computing). Such
// an answer counts as a header store.
func (a *Analyzer) storeScanSettled(query func() storeMode) storeMode {
	saved := a.storeScanCycleHit
	a.storeScanCycleHit = false
	mode := query()
	hit := a.storeScanCycleHit
	a.storeScanCycleHit = saved || hit
	if hit {
		if storeScanTrace && mode < storeHeader {
			fmt.Fprintf(os.Stderr, "SETTLEDHDR mode=%d src=%s frames=%d\n", mode, storeScanTraceOther, len(a.storeScanFrames))
		}
		return storeHeader
	}
	return mode
}

// storeScanSummaryNeverReaches: decl's completed store-flow summary has no edge from parameter
// other into parameter index. A summary still being solved (an assumption) proves nothing.
func (a *Analyzer) storeScanSummaryNeverReaches(decl *ast.FuncDecl, other, index int) bool {
	if decl == nil || funcHasArenaParam(decl) || a.storeFlowInProgress[decl] != nil || a.storeFlowActive[decl] {
		return false
	}
	summary := a.storeFlowSummaryFor(decl)
	return summary != nil && a.storeFlowSummaries[decl] == summary && !summary.reaches(other, index)
}
