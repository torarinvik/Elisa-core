package semantic

import "elisacore/src/ast"

// Creating a thread pool inside a loop is the pool-shaped version of spawn churn: safe,
// but it repeatedly pays worker creation/teardown overhead. The intended shape is to create
// the pool once around the batch, then submit loop work into that persistent pool.
func (a *Analyzer) checkPoolChurn(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.forEachFirstLoopBody(fn.Body, a.flagPoolChurn)
}

func (a *Analyzer) flagPoolChurn(loopBody []ast.Stmt) {
	a.forEachStaticStmtInLoopBody(loopBody, a.flagPoolChurnStmt)
	a.walkPerfLintExprsInLoopBody(loopBody, func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok || call == nil {
			return false
		}
		name := callIdentName(call)
		if name == "" {
			name = callSpecializedIdentName(call)
		}
		if name != "pool_new" {
			return false
		}
		a.perfLint(call.Pos(), "`pool_new` creates a thread pool on every iteration of this loop. Create the pool once around the batch and submit per-iteration work into the persistent pool; pool setup/teardown per item is usually slower than the work it was meant to parallelize. If this is an intentional benchmark or one-shot setup loop, wrap only that loop in `trusted Perf.HotLoop:`")
		return false
	})
}

func (a *Analyzer) flagPoolChurnStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.PoolStmt:
		a.perfLint(s.Pos(), "`pool %s(...)` creates a thread pool on every iteration of this loop. Move the pool scope outside the loop and submit per-iteration work into the persistent pool. If this is an intentional benchmark or one-shot setup loop, wrap only that loop in `trusted Perf.HotLoop:`", s.Name)
	}
}
