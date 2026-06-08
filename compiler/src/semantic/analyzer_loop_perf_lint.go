package semantic

import "elisacore/src/ast"

// forEachFirstLoopBody descends through non-loop statements. At the first loop it finds on
// each path, it calls visit with that loop body and does not descend past that loop here.
// The individual lint may still inspect the full loop body, including nested loops.
func (a *Analyzer) forEachFirstLoopBody(stmts []ast.Stmt, visit func([]ast.Stmt)) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ForStmt:
			visit(s.Body)
		case *ast.WhileStmt:
			visit(s.Body)
		case *ast.IterForStmt:
			visit(s.Body)
		case *ast.IfStmt:
			a.forEachFirstLoopBody(s.Then, visit)
			a.forEachFirstLoopBody(s.Else, visit)
		case *ast.ScopeStmt:
			a.forEachFirstLoopBody(s.Body, visit)
		case *ast.CanStmt:
			if isTrustedPerfHotLoopStmt(s) {
				continue
			}
			a.forEachFirstLoopBody(s.Body, visit)
		case *ast.WithStmt:
			a.forEachFirstLoopBody(s.Body, visit)
		case *ast.RegionStmt:
			a.forEachFirstLoopBody(s.Body, visit)
		case *ast.InStoreStmt:
			a.forEachFirstLoopBody(s.Body, visit)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				a.forEachFirstLoopBody(arm.Body, visit)
			}
		}
	}
}

// forEachStaticStmtInLoopBody walks statements nested inside an already-discovered loop
// body. It is used for source-level block statements such as `pool ...:` and `lock ... as`
// that are not expressions and therefore are invisible to walkStaticStmts.
func (a *Analyzer) forEachStaticStmtInLoopBody(stmts []ast.Stmt, visit func(ast.Stmt)) {
	for _, stmt := range stmts {
		if isTrustedPerfHotLoopStmt(stmt) {
			continue
		}
		visit(stmt)
		switch s := stmt.(type) {
		case *ast.IfStmt:
			a.forEachStaticStmtInLoopBody(s.Then, visit)
			a.forEachStaticStmtInLoopBody(s.Else, visit)
		case *ast.ScopeStmt:
			a.forEachStaticStmtInLoopBody(s.Body, visit)
		case *ast.CanStmt:
			a.forEachStaticStmtInLoopBody(s.Body, visit)
		case *ast.WithStmt:
			a.forEachStaticStmtInLoopBody(s.Body, visit)
		case *ast.RegionStmt:
			a.forEachStaticStmtInLoopBody(s.Body, visit)
		case *ast.InStoreStmt:
			a.forEachStaticStmtInLoopBody(s.Body, visit)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				a.forEachStaticStmtInLoopBody(arm.Body, visit)
			}
		}
	}
}

func (a *Analyzer) walkPerfLintExprsInLoopBody(stmts []ast.Stmt, visitExpr func(ast.Expr) bool) bool {
	for _, stmt := range stmts {
		if a.walkPerfLintExprsInLoopStmt(stmt, visitExpr) {
			return true
		}
	}
	return false
}

func (a *Analyzer) walkPerfLintExprsInLoopStmt(stmt ast.Stmt, visitExpr func(ast.Expr) bool) bool {
	if isTrustedPerfHotLoopStmt(stmt) {
		return false
	}
	switch n := stmt.(type) {
	case *ast.ReturnStmt:
		return a.walkStaticExpr(n.Value, visitExpr)
	case *ast.VarDeclStmt:
		return a.walkStaticExpr(n.Value, visitExpr)
	case *ast.AssignStmt:
		return a.walkStaticExpr(n.Target, visitExpr) || a.walkStaticExpr(n.Value, visitExpr)
	case *ast.AugAssignStmt:
		return a.walkStaticExpr(n.Target, visitExpr) || a.walkStaticExpr(n.Value, visitExpr)
	case *ast.AsRefAssignStmt:
		return a.walkStaticExpr(n.Target, visitExpr) || a.walkStaticExpr(n.Value, visitExpr)
	case *ast.ExprStmt:
		return a.walkStaticExpr(n.Expr, visitExpr)
	case *ast.StaticAssertStmt:
		return a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticExpr(n.Message, visitExpr)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			if a.walkStaticExpr(item.Cond, visitExpr) || a.walkStaticExpr(item.Message, visitExpr) {
				return true
			}
		}
	case *ast.StaticErrorStmt:
		return a.walkStaticExpr(n.Message, visitExpr)
	case *ast.IfStmt:
		if a.walkStaticExpr(n.Cond, visitExpr) || a.walkPerfLintExprsInLoopBody(n.Then, visitExpr) || a.walkPerfLintExprsInLoopBody(n.Else, visitExpr) {
			return true
		}
		for _, elif := range n.Elifs {
			if a.walkStaticExpr(elif.Cond, visitExpr) || a.walkPerfLintExprsInLoopBody(elif.Body, visitExpr) {
				return true
			}
		}
	case *ast.StaticIfStmt:
		if a.walkStaticExpr(n.Cond, visitExpr) || a.walkPerfLintExprsInLoopBody(n.Then, visitExpr) || a.walkPerfLintExprsInLoopBody(n.Else, visitExpr) {
			return true
		}
		for _, elif := range n.Elifs {
			if a.walkStaticExpr(elif.Cond, visitExpr) || a.walkPerfLintExprsInLoopBody(elif.Body, visitExpr) {
				return true
			}
		}
	case *ast.StaticBlockStmt:
		return a.walkPerfLintExprsInLoopBody(n.Body, visitExpr)
	case *ast.WhileStmt:
		return a.walkStaticExpr(n.Cond, visitExpr) || a.walkPerfLintExprsInLoopBody(n.Body, visitExpr)
	case *ast.ForStmt:
		return a.walkStaticExpr(n.Start, visitExpr) || a.walkStaticExpr(n.End, visitExpr) || a.walkStaticExpr(n.Step, visitExpr) || a.walkPerfLintExprsInLoopBody(n.Body, visitExpr)
	case *ast.IterForStmt:
		return a.walkStaticExpr(n.Source, visitExpr) || a.walkStaticExpr(n.WhereFilter, visitExpr) || a.walkStaticExpr(n.Filter, visitExpr) || a.walkPerfLintExprsInLoopBody(n.Body, visitExpr)
	case *ast.MatchStmt:
		if a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticExpr(n.Store, visitExpr) {
			return true
		}
		for _, arm := range n.Arms {
			if a.walkPerfLintExprsInLoopBody(arm.Body, visitExpr) {
				return true
			}
		}
	case *ast.InStoreStmt:
		return a.walkStaticExpr(n.Store, visitExpr) || a.walkPerfLintExprsInLoopBody(n.Body, visitExpr)
	case *ast.CanStmt:
		return a.walkPerfLintExprsInLoopBody(n.Body, visitExpr)
	case *ast.WithStmt:
		for _, arg := range n.Args {
			if a.walkStaticExpr(arg.Value, visitExpr) {
				return true
			}
		}
		return a.walkPerfLintExprsInLoopBody(n.Body, visitExpr)
	default:
		return a.walkStaticStmt(stmt, visitExpr)
	}
	return false
}

func isTrustedPerfHotLoopStmt(stmt ast.Stmt) bool {
	canStmt, ok := stmt.(*ast.CanStmt)
	if !ok || canStmt == nil || !canStmt.SuppressPermissionInference {
		return false
	}
	for _, ref := range canStmt.Permissions {
		if ref.Name == "Perf" && ref.Member == "HotLoop" {
			return true
		}
	}
	return false
}
