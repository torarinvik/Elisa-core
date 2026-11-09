package semantic

import (
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// storeScanPosType is skipped by the name scans: a position's file name is not a binding.
var storeScanPosType = reflect.TypeOf(lexer.Pos{})

// storeMode classifies what a callee may store from one parameter into another's storage.
type storeMode int

const (
	storeNone storeMode = iota // nothing reachable from the source is stored
	// storeViews: only VIEW-ONLY values (built from scalars and plain views) read out of a source
	// that holds no bytes are stored -- they cannot point into the source's own storage, only
	// where its views already point.
	storeViews
	storeElements // only element/field VALUES read out of the source are stored
	storeHeader   // the source value itself (a container header, a view of it) may be stored
)

// narrowStoreMode: a callee storing (part of) its parameter stores at most what the argument
// passed for it carried from the caller's source -- the smaller of the two, unless the argument
// is the source itself (header), which the callee's verdict alone describes.
func narrowStoreMode(argMode, inner storeMode) storeMode {
	if inner == storeNone || argMode == storeHeader || inner < argMode {
		return inner
	}
	return argMode
}

func maxStoreMode(x, y storeMode) storeMode {
	if x > y {
		return x
	}
	return y
}

// calleeStoreModeInto is a DIRECTED, syntactic refinement of the store-flow summary for a
// struct-reference target: what may the callee store from parameter `other` into storage rooted
// at parameter `index`? The store-flow summary unions every pair of written parameters, so
// `parse_one(parser, items)` pushing FROM parser INTO items also reports items reaching parser.
//
// Two taint sets are tracked to a fixpoint: HEADER names may hold the source value itself (the
// parameter, and locals bound from an expression that uses it whole), CONTENT names may hold
// something read out of it (element copies, loop binders over it, anything bound by a
// construct this scan does not model whose header mentions a tainted name). A store into the
// target is any assignment whose target is rooted at a target alias, any method call on a
// target-rooted receiver, and any call passing both a target-rooted and a tainted argument --
// recursed into when the callee resolves, assumed a HEADER store otherwise.
func (a *Analyzer) calleeStoreModeInto(decl *ast.FuncDecl, other, index int) storeMode {
	decl = a.readDecl(decl)
	key := storeScanKey{decl, other, index}
	if mode, ok := a.lookupStoreScanMode(key); ok {
		return mode
	}
	var context *specializationContext
	var previousTargetHit bool
	if a.parallel != nil {
		context = a.parallel.specialization
	}
	if context != nil {
		previousTargetHit = context.storeScanTargetHit
		context.storeScanTargetHit = false
	}
	saved := a.storeScanCycleHit
	a.storeScanCycleHit = false
	restore := a.storeScanBeginSearch()
	mode := a.calleeStoreModeIntoSeen(decl, other, index, map[storeScanKey]bool{})
	restore()
	a.storeScanCycleHit = saved
	targetHit := false
	if context != nil {
		targetHit = context.storeScanTargetHit
		context.storeScanTargetHit = previousTargetHit || targetHit
	}
	// The top-level result of one DFS is the complete least fixpoint for its own key: every
	// body on any cycle through it was scanned within this search.
	a.cacheStoreScan(key, mode)
	if !previousTargetHit && !targetHit {
		a.publishSpecializationStoreScanMode(key, mode)
	}
	return mode
}

type storeScanKey struct {
	decl         *ast.FuncDecl
	other, index int
}

func (a *Analyzer) cacheStoreScan(key storeScanKey, mode storeMode) {
	if a.storeScanCache == nil {
		a.storeScanCache = map[storeScanKey]storeMode{}
	}
	a.storeScanCache[key] = mode
}

// calleeStoreModeIntoSeen memoizes an inner frame only when its search consulted no frame
// above it (region_store_scan_frames.go): a provisional `none` for an open cycle member is not
// final for the frames inside it.
func (a *Analyzer) calleeStoreModeIntoSeen(decl *ast.FuncDecl, other, index int, seen map[storeScanKey]bool) storeMode {
	decl = a.readDecl(decl)
	a.noteSpecializationStoreScanTarget(decl)
	key := storeScanKey{decl, other, index}
	if mode, ok := a.lookupStoreScanMode(key); ok {
		return mode
	}
	if seen[key] {
		// A recursive cycle adds no store the rest of the body does not already show.
		a.storeScanOpenCycle(key)
		return storeNone
	}
	if mode, ok := a.storeScanReuseProvisional(key); ok {
		return mode
	}
	// This is a complete proof independent of the flow summary and recursive
	// body scan. Check it first for valid bodies so program-lived inputs do not
	// build a summary that cannot change the answer.
	if decl != nil && other >= 0 && index >= 0 && other < len(decl.Params) && index < len(decl.Params) && len(decl.Body) != 0 && a.declParamIsProgramLived(decl, other) {
		a.cacheStoreScan(key, storeNone)
		return storeNone
	}
	if a.storeScanSummaryNeverReaches(decl, other, index) && !a.declParamMayEncodeAddress(decl, other) {
		// The (completed, least-fixpoint) store-flow summary proves no flow from other into
		// index at all -- the same proof the call-site gate accepts. Final.
		a.cacheStoreScan(key, storeNone)
		return storeNone
	}
	saved := a.storeScanCycleHit
	a.storeScanCycleHit = false
	mark := a.storeScanPushFrame(key)
	mode := a.calleeStoreModeIntoBody(decl, other, index, seen)
	final := a.storeScanPopFrame(key, mark, mode)
	if final {
		a.cacheStoreScan(key, mode)
	}
	a.storeScanCycleHit = saved || (a.storeScanCycleHit && !final)
	return mode
}
