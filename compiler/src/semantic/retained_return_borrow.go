package semantic

import "elisacore/src/ast"

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
	r.rbInvalid = map[*ast.FuncDecl]bool{}
	var work []*ast.FuncDecl
	for _, sd := range changed {
		if fn, ok := sd.Decl.(*ast.FuncDecl); ok && !r.rbInvalid[fn] {
			r.rbInvalid[fn] = true
			work = append(work, fn)
		}
	}
	for len(work) > 0 {
		fn := work[len(work)-1]
		work = work[:len(work)-1]
		for _, caller := range callers[fn] {
			if !r.rbInvalid[caller] {
				r.rbInvalid[caller] = true
				work = append(work, caller)
			}
		}
	}
	for fn := range r.rbInvalid {
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
