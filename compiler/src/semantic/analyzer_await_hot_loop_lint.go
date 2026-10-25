package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// Completing each concurrent handle inside the same loop that produces work is the common
// way to accidentally serialize a batch: spawn/submit one item, wait for it, then produce
// the next. Prefer a nursery when results are unused; when callers need results, collect
// handles and consume them after the batch. Use TaskGroup when the wait point is explicit.
func (a *Analyzer) checkAwaitHotLoops(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.forEachFirstLoopBody(fn.Body, a.flagAwaitHotLoop)
}

func (a *Analyzer) flagAwaitHotLoop(loopBody []ast.Stmt) {
	a.walkPerfLintExprsInLoopBody(loopBody, func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok || call == nil {
			return false
		}
		name := callIdentName(call)
		if name == "" {
			name = callSpecializedIdentName(call)
		}
		switch name {
		case "pool_await":
			a.perfLint(call.Pos(), "`pool_await` waits for a task on every iteration of this loop. Awaiting per item often serializes the batch; use a nursery when results are unused, or collect pending tasks and await them after the batch. Use a TaskGroup when you need an explicit wait point. If this is intentionally streaming with bounded in-flight work, isolate that policy in a named helper or wrap only that loop in `trusted Perf.HotLoop:`")
		case "task_group_wait_all":
			a.perfLint(call.Pos(), "`task_group_wait_all` waits for a task group on every iteration of this loop. Waiting per item often serializes the batch; use a nursery for ordinary fan-out, or add loop tasks to one TaskGroup and wait once at the boundary when you need an explicit wait point. If this is intentionally windowed streaming, isolate that policy in a named helper or wrap only that loop in `trusted Perf.HotLoop:`")
		case "join", "join_raw":
			if name == "join" && !a.isThreadJoinCall(call) {
				return false
			}
			a.perfLint(call.Pos(), "`%s` joins a thread on every iteration of this loop. Joining per item often serializes the batch; use a nursery for work with no thread handles, or collect thread handles and join after spawning the batch. If this is intentionally bounded streaming, isolate that policy in a named helper or wrap only that loop in `trusted Perf.HotLoop:`", name)
		default:
			return false
		}
		return false
	})
}

func (a *Analyzer) isThreadJoinCall(call *ast.CallExpr) bool {
	if a == nil || call == nil {
		return false
	}
	fnType, _ := a.exprType(call.Func).(*FuncType)
	if fnType == nil || len(fnType.Params) == 0 || fnType.Params[0] == nil {
		return false
	}
	return strings.HasPrefix(fnType.Params[0].String(), "Thread[")
}
