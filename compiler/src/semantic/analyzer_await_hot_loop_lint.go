package semantic

import "elisacore/src/ast"

// Awaiting each task inside the same loop that produces work is the common way to
// accidentally serialize a pool: submit one item, wait for it, submit the next. The fast
// shape is to collect pending tasks or use a task group, then await/wait at the batch
// boundary.
func (a *Analyzer) checkAwaitHotLoops(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.forEachFirstLoopBody(fn.Body, a.flagAwaitHotLoop)
}

func (a *Analyzer) flagAwaitHotLoop(loopBody []ast.Stmt) {
	a.walkStaticStmts(loopBody, func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok || call == nil {
			return false
		}
		name := callIdentName(call)
		if name == "" {
			name = callSpecializedIdentName(call)
		}
		if name != "pool_await" {
			return false
		}
		a.perfLint(call.Pos(), "`pool_await` waits for a task on every iteration of this loop. Awaiting per item often serializes the batch; collect pending tasks in a task group or array, then wait/await at the boundary. If this is intentionally streaming with bounded in-flight work, isolate that policy in a named helper")
		return false
	})
}
