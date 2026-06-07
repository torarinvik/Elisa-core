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
	"spawn1":   true,
	"spawn_raw": true,
}

func (a *Analyzer) checkThreadSpawnChurn(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.findSpawnChurnLoops(fn.Body)
}

// findSpawnChurnLoops descends through non-loop statements; at the first loop it flags every
// per-iteration raw thread spawn anywhere inside that loop (nested loops included).
func (a *Analyzer) findSpawnChurnLoops(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ForStmt:
			a.flagSpawnChurn(s.Body)
		case *ast.WhileStmt:
			a.flagSpawnChurn(s.Body)
		case *ast.IterForStmt:
			a.flagSpawnChurn(s.Body)
		case *ast.IfStmt:
			a.findSpawnChurnLoops(s.Then)
			a.findSpawnChurnLoops(s.Else)
		case *ast.ScopeStmt:
			a.findSpawnChurnLoops(s.Body)
		case *ast.CanStmt:
			a.findSpawnChurnLoops(s.Body)
		case *ast.WithStmt:
			a.findSpawnChurnLoops(s.Body)
		case *ast.RegionStmt:
			a.findSpawnChurnLoops(s.Body)
		case *ast.InStoreStmt:
			a.findSpawnChurnLoops(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				a.findSpawnChurnLoops(arm.Body)
			}
		}
	}
}

func (a *Analyzer) flagSpawnChurn(loopBody []ast.Stmt) {
	a.walkStaticStmts(loopBody, func(e ast.Expr) bool {
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
		a.perfLint(call.Pos(), "`%s` spawns a fresh OS thread on every iteration of this loop. Spawning per iteration was the dominant cost in fine-grained parallel loops. Prefer a persistent thread pool (`pool_submit1` reuses worker threads) or a nursery; spawn a fixed set of workers once and give each a band of the work. If this loop runs a small fixed number of times and each branch is long-lived, this is fine", name)
		return false
	})
}
