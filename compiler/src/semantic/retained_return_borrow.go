package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// Reuse for evaluateLateReturnBorrowFrames in a retained analysis.
//
// A cached return-borrow summary is cut-free, so it depends only on the bodies
// of its function and of the callees whose summaries it consulted. The late
// pass records those consultations per summary and per frame. An update then
// recomputes only the changed functions and everything that transitively
// consulted them, keeps the other final summaries, and replays the other
// frames' diagnostics.

func (r *RetainedAnalysis) noteReturnBorrowDep(fn *ast.FuncDecl) {
	n := len(r.rbStack)
	if n == 0 {
		return
	}
	owner := r.rbStack[n-1]
	if owner == fn {
		return
	}
	deps := r.rbDeps[owner]
	if deps == nil {
		deps = map[*ast.FuncDecl]bool{}
		r.rbDeps[owner] = deps
	}
	deps[fn] = true
}

func (r *RetainedAnalysis) pushReturnBorrowOwner(fn *ast.FuncDecl) {
	r.noteReturnBorrowDep(fn)
	r.rbStack = append(r.rbStack, fn)
}

func (r *RetainedAnalysis) popReturnBorrowOwner() { r.rbStack = r.rbStack[:len(r.rbStack)-1] }

// reusableReturnBorrowSummaries seeds the late pass: nothing on a full
// analysis, the still-valid final summaries during an update.
func (r *RetainedAnalysis) reusableReturnBorrowSummaries() map[*ast.FuncDecl]returnBorrowFlow {
	if !r.rbUpdating {
		return nil
	}
	out := make(map[*ast.FuncDecl]returnBorrowFlow, len(r.rbSummaries))
	for fn, flow := range r.rbSummaries {
		if !r.rbInvalid[fn] {
			out[fn] = flow
		}
	}
	return out
}

func (r *RetainedAnalysis) keepReturnBorrowSummaries(summaries map[*ast.FuncDecl]returnBorrowFlow) {
	r.rbSummaries = make(map[*ast.FuncDecl]returnBorrowFlow, len(summaries))
	for fn, flow := range summaries {
		r.rbSummaries[fn] = flow
	}
}

func (r *RetainedAnalysis) recordLateFrame(fn *ast.FuncDecl, diags []Diagnostic) {
	r.frameDiags[fn] = append([]Diagnostic(nil), diags...)
}

// replayLateFrame reports fn's frame from the last late pass when nothing it
// depends on changed.
func (r *RetainedAnalysis) replayLateFrame(a *Analyzer, fn *ast.FuncDecl) bool {
	if !r.rbUpdating || r.rbInvalid[fn] {
		return false
	}
	diags, ok := r.frameDiags[fn]
	if !ok {
		return false
	}
	for _, d := range diags {
		a.appendDiagnostic(d)
	}
	return true
}

// invalidateReturnBorrow marks the changed functions and every summary or frame
// that transitively consulted them, and moves the kept frame diagnostics by the
// edit's line shifts.
func (r *RetainedAnalysis) invalidateReturnBorrow(changed []scopedDecl, marks map[string][][2]int) {
	callers := map[*ast.FuncDecl][]*ast.FuncDecl{}
	for owner, deps := range r.rbDeps {
		for dep := range deps {
			callers[dep] = append(callers[dep], owner)
		}
	}
	r.rbChanged = r.rbChanged[:0]
	for _, sd := range changed {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok {
			r.rbChanged = append(r.rbChanged, fn)
		}
	}
	r.rbCallers = callers
	r.rbInvalid = returnBorrowCallerClosure(r.rbChanged, callers)
	// The pre-update deps of the transitive callers stay until the late pass
	// narrows the set; only the changed functions' deps are recorded afresh now.
	r.rbStaleDeps = map[*ast.FuncDecl]map[*ast.FuncDecl]bool{}
	for fn := range r.rbInvalid {
		r.rbStaleDeps[fn] = r.rbDeps[fn]
		delete(r.rbDeps, fn) // recorded afresh when recomputed
	}
	if len(marks) != 0 {
		for _, diags := range r.frameDiags {
			for i := range diags {
				diags[i].Pos = retainedShiftPos(diags[i].Pos, marks)
			}
		}
	}
	r.LastStats.Invalidated = len(r.rbInvalid)
}

// returnBorrowCallerClosure is roots plus every function that transitively
// consulted one of them.
func returnBorrowCallerClosure(roots []*ast.FuncDecl, callers map[*ast.FuncDecl][]*ast.FuncDecl) map[*ast.FuncDecl]bool {
	out := map[*ast.FuncDecl]bool{}
	work := make([]*ast.FuncDecl, 0, len(roots))
	for _, fn := range roots {
		if !out[fn] {
			out[fn] = true
			work = append(work, fn)
		}
	}
	for len(work) > 0 {
		fn := work[len(work)-1]
		work = work[:len(work)-1]
		for _, caller := range callers[fn] {
			if !out[caller] {
				out[caller] = true
				work = append(work, caller)
			}
		}
	}
	return out
}

// narrowReturnBorrowInvalidation is the late pass's early cutoff. A caller's
// summary and frame depend on a callee only through the callee's summary, so
// a changed function whose recomputed cut-free summary equals its previous one
// invalidates nothing beyond itself. Each changed function's summary is
// recomputed first; callers are then invalidated only from those whose summary
// differs or could not be cached. Any diagnostic from the probe abandons the
// cutoff and keeps the conservative set.
func (r *RetainedAnalysis) narrowReturnBorrowInvalidation(a *Analyzer) {
	if !r.rbCutoff || len(r.rbChanged) == 0 {
		return
	}
	before := len(a.diagnostics)
	savedFn := a.currentFuncDecl
	a.currentFuncDecl = nil
	for _, fn := range r.rbChanged {
		a.returnBorrowFlowForFunc(fn, map[*ast.FuncDecl]bool{})
	}
	a.currentFuncDecl = savedFn
	if len(a.diagnostics) != before {
		return
	}
	var dirty []*ast.FuncDecl
	for _, fn := range r.rbChanged {
		fresh, cached := a.returnBorrowFuncSummaries[fn]
		old, had := r.rbSummaries[fn]
		if !cached || !had || !reflect.DeepEqual(normalizedReturnBorrowFlow(fresh), normalizedReturnBorrowFlow(old)) {
			dirty = append(dirty, fn)
		}
	}
	narrowed := returnBorrowCallerClosure(dirty, r.rbCallers)
	for _, fn := range r.rbChanged {
		narrowed[fn] = true
	}
	for fn := range r.rbInvalid {
		if narrowed[fn] {
			continue
		}
		// Valid again: its previous deps and final summary still hold.
		if deps := r.rbStaleDeps[fn]; deps != nil {
			if cur := r.rbDeps[fn]; cur != nil {
				for d := range deps {
					cur[d] = true
				}
			} else {
				r.rbDeps[fn] = deps
			}
		}
		if flow, ok := r.rbSummaries[fn]; ok {
			if _, fresh := a.returnBorrowFuncSummaries[fn]; !fresh {
				a.returnBorrowFuncSummaries[fn] = flow
			}
		}
	}
	r.rbInvalid = narrowed
	r.LastStats.Invalidated = len(narrowed)
}

func normalizedReturnBorrowFlow(f returnBorrowFlow) returnBorrowFlow {
	if len(f.Params) == 0 {
		f.Params = nil
	}
	if len(f.Contents) == 0 {
		f.Contents = nil
	}
	return f
}
