package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// Fill and return-element summaries exist only for functions analyzed before their callers. A call
// cycle has no such order, so a recursive function's summary is the least fixpoint of its body's
// own analysis: start from the optimistic assumption (returns nothing region-dependent, adopts no
// caller arena), analyze the cycle quietly, and repeat with the published results until they stop
// changing. Whatever does not converge falls back to the coarse per-container region, which
// over-reports but never accepts a dangling store.

const summaryConvergenceRounds = 4

type summarySnapshot struct {
	hasReturn bool
	ret       regionRefState
	analyzed  bool
	fresh     bool
}

// funcIsCyclic reports whether fn lies on a call cycle (by callee name, over every overload). Call
// edges are collected lazily and cached per declaration, and only the part of the call graph
// reachable from fn is walked, so a program pays for the bodies it is asked about.
func (a *Analyzer) funcIsCyclic(fn *ast.FuncDecl) bool {
	if fn == nil {
		return false
	}
	if a.cyclicFuncSet == nil {
		a.cyclicFuncSet = map[*ast.FuncDecl]bool{}
	}
	if known, ok := a.cyclicFuncSet[fn]; ok {
		return known
	}
	seen := map[*ast.FuncDecl]bool{}
	work := append([]*ast.FuncDecl(nil), a.callEdgesOf(fn)...)
	cyclic := false
	for len(work) > 0 && !cyclic {
		next := work[len(work)-1]
		work = work[:len(work)-1]
		if next == fn {
			cyclic = true
			break
		}
		if seen[next] {
			continue
		}
		seen[next] = true
		work = append(work, a.callEdgesOf(next)...)
	}
	a.cyclicFuncSet[fn] = cyclic
	return cyclic
}

// callEdgesOf returns the declarations fn's body may call, resolved by last name segment.
func (a *Analyzer) callEdgesOf(decl *ast.FuncDecl) []*ast.FuncDecl {
	if edges, ok := a.callEdgeCache[decl]; ok {
		return edges
	}
	if a.callEdgeCache == nil {
		a.callEdgeCache = map[*ast.FuncDecl][]*ast.FuncDecl{}
	}
	if a.callDeclsByName == nil {
		a.callDeclsByName = map[string][]*ast.FuncDecl{}
		for d := range a.funcDeclSymbols {
			if d != nil && d.Body != nil {
				name := fillMayAdoptLastSegment(d.Name)
				a.callDeclsByName[name] = append(a.callDeclsByName[name], d)
			}
		}
	}
	var edges []*ast.FuncDecl
	seen := map[*ast.FuncDecl]bool{}
	if decl != nil && decl.Body != nil {
		fillMayAdoptWalk(reflect.ValueOf(decl.Body), func(v any) {
			call, ok := v.(*ast.CallExpr)
			if !ok || call == nil {
				return
			}
			target := call.Func
			if special, ok := target.(*ast.SpecializeExpr); ok && special != nil {
				target = special.Operand
			}
			name := ""
			switch callee := target.(type) {
			case *ast.Ident:
				name = callee.Name
			case *ast.FieldExpr:
				name = callee.Field
			}
			for _, callee := range a.callDeclsByName[fillMayAdoptLastSegment(name)] {
				if !seen[callee] {
					seen[callee] = true
					edges = append(edges, callee)
				}
			}
		})
	}
	a.callEdgeCache[decl] = edges
	return edges
}

// assumeSummaryForActive installs the optimistic summary for a function whose analysis is on the
// stack, inside a convergence round only.
func (a *Analyzer) assumeSummaryForActive(decl *ast.FuncDecl) {
	if a.convergeDepth == 0 || !a.summaryAnalyzable(decl) {
		return
	}
	if a.ambientFillAnalyzed == nil {
		a.ambientFillAnalyzed = map[*ast.FuncDecl]bool{}
	}
	a.ambientFillAnalyzed[decl] = true
	a.convMembers[decl] = true
	if _, has := a.returnElementSummaries[decl]; !has {
		if a.returnElementSummaries == nil {
			a.returnElementSummaries = map[*ast.FuncDecl]regionRefState{}
		}
		a.returnElementSummaries[decl] = regionRefState{}
	}
}

func (a *Analyzer) snapshotSummary(decl *ast.FuncDecl) summarySnapshot {
	ret, has := a.returnElementSummaries[decl]
	return summarySnapshot{hasReturn: has, ret: ret, analyzed: a.ambientFillAnalyzed[decl], fresh: a.ambientFillFresh[decl]}
}

func (s summarySnapshot) equal(other summarySnapshot) bool {
	return s.hasReturn == other.hasReturn && s.analyzed == other.analyzed && s.fresh == other.fresh &&
		(!s.hasReturn || reflect.DeepEqual(s.ret, other.ret))
}

// convergeSummaries solves the summaries of root and every function analyzed on its behalf.
func (a *Analyzer) convergeSummaries(root *ast.FuncDecl) {
	a.convergeDepth++
	savedMembers := a.convMembers
	a.convMembers = map[*ast.FuncDecl]bool{root: true}
	defer func() {
		a.convergeDepth--
		a.convMembers = savedMembers
	}()
	converged := false
	for round := 0; round < summaryConvergenceRounds && !converged; round++ {
		before := map[*ast.FuncDecl]summarySnapshot{}
		for member := range a.convMembers {
			before[member] = a.snapshotSummary(member)
			// Re-analyze every member against the current assumptions; the published summary
			// stays in place as the assumption until the member's new one replaces it.
			a.ambientFillAnalyzed[member] = false
			delete(a.ambientFillFresh, member)
		}
		a.analyzeQuietly(root)
		converged = true
		for member := range a.convMembers {
			prior, seen := before[member]
			if !seen || !prior.equal(a.snapshotSummary(member)) {
				converged = false
				break
			}
		}
		// A member first met this round changes the member set: settle it next round.
		if len(before) != len(a.convMembers) {
			converged = false
		}
	}
	if !converged {
		for member := range a.convMembers {
			delete(a.returnElementSummaries, member)
			delete(a.ambientFillAnalyzed, member)
			delete(a.ambientFillFresh, member)
		}
	}
}

// summaryRelevant limits convergence to functions whose summaries feed a store or return check:
// one that returns a container able to hold region references, or takes a writable such container.
func (a *Analyzer) summaryRelevant(fn *ast.FuncDecl) bool {
	sym := a.funcDeclSymbols[fn]
	if sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil {
		return false
	}
	if fnType.Return != nil && !isVoidType(fnType.Return) && a.containerMayHoldRegionRefs(stripRefForBounds(fnType.Return)) {
		return true
	}
	for _, param := range fnType.Params {
		if a.returnBorrowWritableParam(param) && a.containerMayHoldRegionRefs(stripRefForBounds(param)) {
			return true
		}
	}
	return false
}
