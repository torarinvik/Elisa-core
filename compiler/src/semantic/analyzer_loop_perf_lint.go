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
