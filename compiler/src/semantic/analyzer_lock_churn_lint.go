package semantic

import "elisacore/src/ast"

// The lock-churn lint catches high-frequency lock acquisition inside loops. A lock around a
// whole batch can be the right shape; lock/unlock per element is often the concurrency
// equivalent of per-iteration allocation churn: safe, but a scalability cliff.
func (a *Analyzer) checkLockChurn(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.forEachFirstLoopBody(fn.Body, a.flagLockChurn)
}

func (a *Analyzer) flagLockChurn(loopBody []ast.Stmt) {
	a.forEachStaticStmtInLoopBody(loopBody, a.flagLockChurnStmt)
	a.walkPerfLintExprsInLoopBody(loopBody, func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok || call == nil {
			return false
		}
		name := callIdentName(call)
		if name == "" {
			name = callSpecializedIdentName(call)
		}
		if name != "mutex_lock" {
			return false
		}
		a.perfLint(call.Pos(), "`mutex_lock` acquires a lock on every iteration of this loop. Prefer batching the locked work, sharding the state, using a reduction/local accumulator, or moving synchronization to a coarser protocol boundary. If each iteration is intentionally long-lived or externally synchronized, this is fine")
		return false
	})
}

func (a *Analyzer) flagLockChurnStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.LockStmt:
		a.perfLint(s.Pos(), "`lock ... as %s` acquires a lock on every iteration of this loop. Prefer batching the locked work, sharding the state, using a reduction/local accumulator, or moving synchronization to a coarser protocol boundary", s.GuardName)
	}
}
