package semantic

import "elisacore/src/ast"

// A task group is meant to collect a batch of submitted work. Creating one per loop
// iteration usually means the batching boundary is inside-out: each item gets its own
// coordination object instead of one group collecting the whole loop.
func (a *Analyzer) checkTaskGroupChurn(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.forEachFirstLoopBody(fn.Body, a.flagTaskGroupChurn)
}

func (a *Analyzer) flagTaskGroupChurn(loopBody []ast.Stmt) {
	a.walkPerfLintExprsInLoopBody(loopBody, func(e ast.Expr) bool {
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
