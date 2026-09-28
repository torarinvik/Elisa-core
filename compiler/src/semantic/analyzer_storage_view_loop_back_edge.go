package semantic

import (
	"sort"

	"elisacore/src/ast"
)

// storageViewLoopUseFrame records, for one loop, the first use of each storage view declared
// outside the loop. A view that is valid at loop entry but invalidated on the back edge (the
// body's fall-through or a `continue` grew its container) is stale when the NEXT iteration reaches
// that first use: the forward walk sees the use only once, in the entry state, so without this
// check a reference read at the top of the body after a push at the bottom was accepted
// (use-after-realloc on the second iteration).
type storageViewLoopUseFrame struct {
	outer    *Scope
	body     []ast.Stmt
	firstUse map[*Symbol]storageViewLoopUse
}

type storageViewLoopUse struct {
	expr       ast.Expr
	name       string
	validAtUse bool
}

// pushStorageViewLoopUseFrame opens a frame for a loop whose re-evaluated parts (while condition,
// iterable filters, body) are analyzed next; outer is the scope enclosing the loop.
func (a *Analyzer) pushStorageViewLoopUseFrame(outer *Scope, body []ast.Stmt) {
	a.storageViewLoopUseFrames = append(a.storageViewLoopUseFrames, storageViewLoopUseFrame{outer: outer, body: body})
}

func (a *Analyzer) popStorageViewLoopUseFrame() storageViewLoopUseFrame {
	n := len(a.storageViewLoopUseFrames)
	if n == 0 {
		return storageViewLoopUseFrame{}
	}
	frame := a.storageViewLoopUseFrames[n-1]
	a.storageViewLoopUseFrames = a.storageViewLoopUseFrames[:n-1]
	return frame
}

// noteStorageViewLoopUse records a view use in every enclosing loop frame (a use inside an inner
// loop is also reached by each outer loop's next iteration).
func (a *Analyzer) noteStorageViewLoopUse(sym *Symbol, expr ast.Expr, name string, valid bool) {
	for i := range a.storageViewLoopUseFrames {
		frame := &a.storageViewLoopUseFrames[i]
		if _, seen := frame.firstUse[sym]; seen {
			continue
		}
		if frame.firstUse == nil {
			frame.firstUse = map[*Symbol]storageViewLoopUse{}
		}
		frame.firstUse[sym] = storageViewLoopUse{expr: expr, name: name, validAtUse: valid}
	}
}

// noteLoopJumpStorageViewState joins the storage-view state at a `continue` into the innermost
// loop's back edge.
func (a *Analyzer) noteLoopJumpStorageViewState(frame *loopAffineFrame) {
	state := a.cloneStorageViewDeps()
	if state == nil {
		state = map[*Symbol]storageViewDependencyState{}
	}
	frame.storageContinued = mergeStorageViewDependencyStates(frame.storageContinued, state)
}

// checkStorageViewLoopBackEdge pops the loop's use frame and reports each first use of an outer
// view that was valid when reached on the first iteration but is invalid on the back edge, unless
// a top-level body statement before that use rebinds the view on every iteration. It must run
// before finishLoopAffineFrame pops the loop's affine frame (which holds the `continue` states).
func (a *Analyzer) checkStorageViewLoopBackEdge(bodyDeps map[*Symbol]storageViewDependencyState, bodyExits bool) {
	frame := a.popStorageViewLoopUseFrame()
	if len(frame.firstUse) == 0 || frame.outer == nil {
		return
	}
	var backEdge map[*Symbol]storageViewDependencyState
	if n := len(a.loopAffineFrames); n > 0 && a.loopAffineFrames[n-1].depth == a.loopDepth+1 {
		backEdge = mergeStorageViewDependencyStates(nil, a.loopAffineFrames[n-1].storageContinued)
	}
	if !bodyExits {
		backEdge = mergeStorageViewDependencyStates(backEdge, bodyDeps)
	}
	if len(backEdge) == 0 {
		return
	}
	type staleUse struct {
		sym *Symbol
		use storageViewLoopUse
		dep storageViewDependencyState
	}
	var stale []staleUse
	for sym, use := range frame.firstUse {
		if !use.validAtUse {
			continue // already reported in the entry state
		}
		if found, ok := frame.outer.Lookup(sym.Name); !ok || found != sym {
			continue // declared in the body: fresh every iteration
		}
		dep, ok := backEdge[sym]
		if !ok || dep.Valid {
			continue
		}
		if storageViewReboundBefore(frame.body, sym.Name, use.expr.Pos().Offset) {
			continue
		}
		stale = append(stale, staleUse{sym: sym, use: use, dep: dep})
	}
	sort.Slice(stale, func(i, j int) bool { return stale[i].use.expr.Pos().Offset < stale[j].use.expr.Pos().Offset })
	for _, s := range stale {
		a.flagStaleStorageViewUse(s.use.expr, s.use.name, s.dep)
	}
}

// storageViewReboundBefore reports whether a top-level statement of body that ends before
// useOffset assigns the view name (`r = ...` or an as-ref assignment). Only top-level statements
// run on every iteration; a rebind inside a branch does not cover the other path.
func storageViewReboundBefore(body []ast.Stmt, name string, useOffset int) bool {
	for i, stmt := range body {
		if i+1 >= len(body) || body[i+1].Pos().Offset > useOffset {
			return false // stmt contains (or follows) the use
		}
		var target ast.Expr
		switch n := stmt.(type) {
		case *ast.AssignStmt:
			if !n.Optional && !n.WriteThrough { // `r <- 5` stores through r; it does not rebind it
				target = n.Target
			}
		case *ast.AsRefAssignStmt:
			target = n.Target
		}
		if ident, ok := stripOptimizationParens(target).(*ast.Ident); ok && ident != nil && ident.Name == name {
			return true
		}
	}
	return false
}
