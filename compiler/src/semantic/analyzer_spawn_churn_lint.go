package semantic

import "elisacore/src/ast"

// The thread-spawn-churn lint (pit-of-success for concurrency). Each `spawn1`/`spawn_raw`
// creates a fresh OS thread; doing so once per loop iteration is the per-iteration-thread
// anti-pattern that dominated wall-clock in the parallel-fluid stress test (a pthread_create
// per pass). The fast, safe shape is a persistent thread pool (`pool_submit1` reuses worker
// threads) or a nursery. Pool submission is deliberately NOT flagged — that IS the batch tool,
// the exact analogue of `push` vs per-iteration `new` in the allocation-churn lint.

// rawThreadSpawnNames are the user-facing primitives that each create a new OS thread.
var rawThreadSpawnNames = map[string]bool{
	"spawn1":    true,
	"spawn_raw": true,
}

func (a *Analyzer) checkThreadSpawnChurn(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.forEachFirstLoopBody(fn.Body, a.flagSpawnChurn)
}

func (a *Analyzer) flagSpawnChurn(loopBody []ast.Stmt) {
	a.walkPerfLintExprsInLoopBody(loopBody, func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok || call == nil {
			return false
		}
		name := callIdentName(call)
		if name == "" {
			name = callSpecializedIdentName(call)
		}
		if !rawThreadSpawnNames[name] {
			return false
		}
		a.perfLint(call.Pos(), "`%s` spawns a fresh OS thread on every iteration of this loop. Spawning per iteration was the dominant cost in fine-grained parallel loops. Prefer `nursery workers(N):` or create a pool/fixed worker set around the batch and submit work inside the loop. If this is an intentional benchmark or bounded low-level policy, wrap only that loop in `trusted Perf.HotLoop:`", name)
		return false
	})
}
