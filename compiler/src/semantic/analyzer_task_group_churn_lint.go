package semantic

import "elisacore/src/ast"

// A task group is meant to collect a batch of submitted work. For ordinary fan-out,
// the nursery owns that group and joins at the block boundary. Creating a group per loop
// iteration usually means the batching boundary is inside-out.
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
		a.perfLint(call.Pos(), "`task_group_new` creates a task group on every iteration of this loop. Use a nursery for ordinary fan-out when results are not needed; retain task handles and await them after the batch when results are needed. If results are discarded but you need a manual wait point, use one TaskGroup around the batch. Per-item groups add coordination overhead without increasing parallelism. Use `nursery on(pool):` to reuse a caller-owned pool across batches. If this is an intentional batching/window policy, isolate it in a named helper or wrap only that loop in `trusted Perf.HotLoop:`")
		return false
	})
}
