package semantic

import "elisacore/src/ast"

// Creating a thread pool inside a loop is the pool-shaped version of spawn churn: safe,
// but it repeatedly pays worker creation/teardown overhead. The intended shape is to create
// the pool once around the batch, then submit loop work into that persistent pool.
func (a *Analyzer) checkPoolChurn(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.findPoolChurnLoops(fn.Body)
}

func (a *Analyzer) findPoolChurnLoops(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ForStmt:
			a.flagPoolChurn(s.Body)
		case *ast.WhileStmt:
			a.flagPoolChurn(s.Body)
		case *ast.IterForStmt:
			a.flagPoolChurn(s.Body)
		case *ast.IfStmt:
			a.findPoolChurnLoops(s.Then)
			a.findPoolChurnLoops(s.Else)
		case *ast.ScopeStmt:
			a.findPoolChurnLoops(s.Body)
		case *ast.CanStmt:
			a.findPoolChurnLoops(s.Body)
		case *ast.WithStmt:
			a.findPoolChurnLoops(s.Body)
		case *ast.RegionStmt:
			a.findPoolChurnLoops(s.Body)
		case *ast.InStoreStmt:
			a.findPoolChurnLoops(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				a.findPoolChurnLoops(arm.Body)
			}
		}
	}
}

func (a *Analyzer) flagPoolChurn(loopBody []ast.Stmt) {
	for _, stmt := range loopBody {
		a.flagPoolChurnStmt(stmt)
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
		if name != "pool_new" {
			return false
		}
		a.perfLint(call.Pos(), "`pool_new` creates a thread pool on every iteration of this loop. Create the pool once around the batch and submit per-iteration work into the persistent pool; pool setup/teardown per item is usually slower than the work it was meant to parallelize")
		return false
	})
}

func (a *Analyzer) flagPoolChurnStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.PoolStmt:
		a.perfLint(s.Pos(), "`pool %s(...)` creates a thread pool on every iteration of this loop. Move the pool scope outside the loop and submit per-iteration work into the persistent pool", s.Name)
	case *ast.IfStmt:
		for _, child := range s.Then {
			a.flagPoolChurnStmt(child)
		}
		for _, child := range s.Else {
			a.flagPoolChurnStmt(child)
		}
	case *ast.ScopeStmt:
		for _, child := range s.Body {
			a.flagPoolChurnStmt(child)
		}
	case *ast.CanStmt:
		for _, child := range s.Body {
			a.flagPoolChurnStmt(child)
		}
	case *ast.WithStmt:
		for _, child := range s.Body {
			a.flagPoolChurnStmt(child)
		}
	case *ast.RegionStmt:
		for _, child := range s.Body {
			a.flagPoolChurnStmt(child)
		}
	case *ast.InStoreStmt:
		for _, child := range s.Body {
			a.flagPoolChurnStmt(child)
		}
	case *ast.MatchStmt:
		for _, arm := range s.Arms {
			for _, child := range arm.Body {
				a.flagPoolChurnStmt(child)
			}
		}
	}
}
