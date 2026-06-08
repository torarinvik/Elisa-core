package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// Completing each concurrent handle inside the same loop that produces work is the common
// way to accidentally serialize a batch: spawn/submit one item, wait for it, then produce
// the next. The fast shape is to collect handles or use a task group, then join/await/wait
// at the batch boundary.
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
		switch name {
		case "pool_await":
			a.perfLint(call.Pos(), "`pool_await` waits for a task on every iteration of this loop. Awaiting per item often serializes the batch; collect pending tasks in a task group or array, then wait/await at the boundary. If this is intentionally streaming with bounded in-flight work, isolate that policy in a named helper")
		case "task_group_wait_all":
			a.perfLint(call.Pos(), "`task_group_wait_all` waits for a task group on every iteration of this loop. Waiting per item often serializes the batch; add loop tasks to one group and wait once at the boundary. If this is intentionally windowed streaming, isolate that policy in a named helper")
		case "join", "join_raw":
			if name == "join" && !a.isThreadJoinCall(call) {
				return false
			}
			a.perfLint(call.Pos(), "`%s` joins a thread on every iteration of this loop. Joining per item often serializes the batch; collect thread handles and join after spawning the batch, or use a nursery/pool. If this is intentionally bounded streaming, isolate that policy in a named helper", name)
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
	fnType, _ := a.exprTypes[call.Func].(*FuncType)
	if fnType == nil || len(fnType.Params) == 0 || fnType.Params[0] == nil {
		return false
	}
	return strings.HasPrefix(fnType.Params[0].String(), "Thread[")
}
