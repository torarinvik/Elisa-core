package semantic

import "elisacore/src/ast"

// The lock-churn lint catches high-frequency lock acquisition inside loops. A lock around a
// whole batch can be the right shape; lock/unlock per element is often the concurrency
// equivalent of per-iteration allocation churn: safe, but a scalability cliff.
func (a *Analyzer) checkLockChurn(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.findLockChurnLoops(fn.Body)
}

func (a *Analyzer) findLockChurnLoops(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ForStmt:
			a.flagLockChurn(s.Body)
		case *ast.WhileStmt:
			a.flagLockChurn(s.Body)
		case *ast.IterForStmt:
			a.flagLockChurn(s.Body)
		case *ast.IfStmt:
			a.findLockChurnLoops(s.Then)
			a.findLockChurnLoops(s.Else)
		case *ast.ScopeStmt:
			a.findLockChurnLoops(s.Body)
		case *ast.CanStmt:
			a.findLockChurnLoops(s.Body)
		case *ast.WithStmt:
			a.findLockChurnLoops(s.Body)
		case *ast.RegionStmt:
			a.findLockChurnLoops(s.Body)
		case *ast.InStoreStmt:
			a.findLockChurnLoops(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				a.findLockChurnLoops(arm.Body)
			}
		}
	}
}

func (a *Analyzer) flagLockChurn(loopBody []ast.Stmt) {
	for _, stmt := range loopBody {
		a.flagLockChurnStmt(stmt)
	}
	a.walkStaticStmts(loopBody, func(e ast.Expr) bool {
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
	case *ast.IfStmt:
		for _, child := range s.Then {
			a.flagLockChurnStmt(child)
		}
		for _, child := range s.Else {
			a.flagLockChurnStmt(child)
		}
	case *ast.ScopeStmt:
		for _, child := range s.Body {
			a.flagLockChurnStmt(child)
		}
	case *ast.CanStmt:
		for _, child := range s.Body {
			a.flagLockChurnStmt(child)
		}
	case *ast.WithStmt:
		for _, child := range s.Body {
			a.flagLockChurnStmt(child)
		}
	case *ast.RegionStmt:
		for _, child := range s.Body {
			a.flagLockChurnStmt(child)
		}
	case *ast.InStoreStmt:
		for _, child := range s.Body {
			a.flagLockChurnStmt(child)
		}
	case *ast.MatchStmt:
		for _, arm := range s.Arms {
			for _, child := range arm.Body {
				a.flagLockChurnStmt(child)
			}
		}
	}
}
