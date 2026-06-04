package semantic

import "elisacore/src/ast"

// The allocation-churn lint (docs/70). Individual `new` boxing inside a loop allocates one
// value per iteration — the per-object-allocation pattern that batch allocation avoids. It
// is the complement of the pointer-graph lint: that flags raw graph *structure*, this flags
// building such a graph (or any heap garbage) one box at a time.
//
// It deliberately does NOT flag `push` into a collection (accumulation is the intended way
// to build one) nor packed-node sugar (`new` with NodeSugar — that IS the batch
// store style). Only plain discretionary `new[r] value` per iteration is nudged, and only
// as a warning: if each iteration genuinely needs a distinct fresh box that is the right
// tool, but most per-iteration `new` wants a Store / preallocated darray instead.

func (a *Analyzer) checkAllocationChurn(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.findChurnLoops(fn.Body)
}

// findChurnLoops descends through non-loop statements; at the first loop it flags every
// per-iteration `new` anywhere inside that loop (nested loops included).
func (a *Analyzer) findChurnLoops(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ForStmt:
			a.flagChurnAllocations(s.Body)
		case *ast.WhileStmt:
			a.flagChurnAllocations(s.Body)
		case *ast.IterForStmt:
			a.flagChurnAllocations(s.Body)
		case *ast.IfStmt:
			a.findChurnLoops(s.Then)
			a.findChurnLoops(s.Else)
		case *ast.ScopeStmt:
			a.findChurnLoops(s.Body)
		case *ast.CanStmt:
			a.findChurnLoops(s.Body)
		case *ast.WithStmt:
			a.findChurnLoops(s.Body)
		case *ast.RegionStmt:
			a.findChurnLoops(s.Body)
		case *ast.InStoreStmt:
			a.findChurnLoops(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				a.findChurnLoops(arm.Body)
			}
		}
	}
}

func (a *Analyzer) flagChurnAllocations(loopBody []ast.Stmt) {
	a.walkStaticStmts(loopBody, func(e ast.Expr) bool {
		alloc, ok := e.(*ast.AllocExpr)
		if !ok || alloc == nil || alloc.NodeSugar {
			return false
		}
		a.warnf(alloc.Position, "`new` boxes a value on every iteration of this loop (per-object allocation). Prefer one batch allocation — push into a preallocated `darray`/`Store`, allocate into a packed enum, or hoist the allocation and reuse it. If each iteration needs a genuinely distinct fresh box, this is fine")
		return false
	})
}
