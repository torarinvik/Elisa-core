package semantic

import "elisacore/src/ast"

// A task group is meant to collect a batch of submitted work. Creating one per loop
// iteration usually means the batching boundary is inside-out: each item gets its own
// coordination object instead of one group collecting the whole loop.
func (a *Analyzer) checkTaskGroupChurn(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.findTaskGroupChurnLoops(fn.Body)
}

func (a *Analyzer) findTaskGroupChurnLoops(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ForStmt:
			a.flagTaskGroupChurn(s.Body)
		case *ast.WhileStmt:
			a.flagTaskGroupChurn(s.Body)
		case *ast.IterForStmt:
			a.flagTaskGroupChurn(s.Body)
		case *ast.IfStmt:
			a.findTaskGroupChurnLoops(s.Then)
			a.findTaskGroupChurnLoops(s.Else)
		case *ast.ScopeStmt:
			a.findTaskGroupChurnLoops(s.Body)
		case *ast.CanStmt:
			a.findTaskGroupChurnLoops(s.Body)
		case *ast.WithStmt:
			a.findTaskGroupChurnLoops(s.Body)
		case *ast.RegionStmt:
			a.findTaskGroupChurnLoops(s.Body)
		case *ast.InStoreStmt:
			a.findTaskGroupChurnLoops(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				a.findTaskGroupChurnLoops(arm.Body)
			}
		}
	}
}

func (a *Analyzer) flagTaskGroupChurn(loopBody []ast.Stmt) {
	a.walkStaticStmts(loopBody, func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok || call == nil {
			return false
		}
		name := callIdentName(call)
		if name == "" {
			name = callSpecializedIdentName(call)
		}
		if name != "task_group_new" {
			return false
		}
		a.perfLint(call.Pos(), "`task_group_new` creates a task group on every iteration of this loop. Create one task group around the batch, add per-iteration tasks to it, and wait once at the boundary; per-item groups add coordination overhead without increasing parallelism")
		return false
	})
}
