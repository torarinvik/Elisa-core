package flowshape

import "elisacore/src/ast"

// HasDirectProgress reports whether any DIRECT (top-level, non-branch) statement of the loop
// body is unconditional progress — a carried-binding mutation, an exit, or a bare mutating call —
// which runs every iteration and so guarantees the loop advances regardless of its branches. A
// value-producing read declaration (`c: char = source[i].char()`) is deliberately NOT progress:
// it is the scanner's per-iteration lookahead, not an advance.
func HasDirectProgress(stmts []ast.Stmt, carried map[string]bool) bool {
	for _, stmt := range stmts {
		if flowIsUnconditionalProgress(stmt, carried) {
			return true
		}
	}
	return false
}

// flowIsUnconditionalProgress is the strict progress test for a top-level statement: only a
// carried-binding mutation, an exit, or a bare call/raise statement guarantees the loop advances.
func flowIsUnconditionalProgress(stmt ast.Stmt, carried map[string]bool) bool {
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		return carried[identNameOf(n.Target)]
	case *ast.AugAssignStmt:
		return carried[identNameOf(n.Target)]
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
		return true
	case *ast.ExprStmt:
		return flowExprPerformsCall(n.Expr)
	}
	return false
}

// StmtsMakeProgress reports whether a block can make progress on some path within it: it holds
// a progress statement directly, or a branch whose some arm makes progress. Descends through
// branch/scope wrappers but not into nested loops (reaching one at all is itself an effect, so a
// nested loop counts as progress).
func StmtsMakeProgress(stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		if flowIsProgressStmt(stmt) {
			return true
		}
		switch n := stmt.(type) {
		case *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt:
			return true // running an inner loop is itself progress
		case *ast.IfStmt:
			if StmtsMakeProgress(n.Then) {
				return true
			}
			for _, elif := range n.Elifs {
				if StmtsMakeProgress(elif.Body) {
					return true
				}
			}
			if StmtsMakeProgress(n.Else) {
				return true
			}
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				if StmtsMakeProgress(arm.Body) {
					return true
				}
			}
		case *ast.ScopeStmt:
			if StmtsMakeProgress(n.Body) {
				return true
			}
		case *ast.CanStmt:
			if StmtsMakeProgress(n.Body) {
				return true
			}
		case *ast.InStoreStmt:
			if StmtsMakeProgress(n.Body) {
				return true
			}
		case *ast.RegionStmt:
			if StmtsMakeProgress(n.Body) {
				return true
			}
		}
	}
	return false
}

// flowIsProgressStmt reports whether a single statement, on its own, makes progress: any
// assignment (changes state), any exit, or any statement that performs a call (which may advance a
// cursor through a reference).
func flowIsProgressStmt(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.AssignStmt, *ast.AugAssignStmt, *ast.TupleBindStmt:
		return true
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
		return true
	case *ast.ExprStmt:
		return flowExprPerformsCall(n.Expr)
	case *ast.VarDeclStmt:
		return flowExprPerformsCall(n.Value)
	}
	return false
}

// flowExprPerformsCall reports whether evaluating the expression performs a call or raises — the
// effects R5 trusts as potential cursor advances. A raise is an exit; a call may mutate through a
// reference argument.
func flowExprPerformsCall(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.CallExpr:
		return true
	case *ast.RaiseExpr:
		return true
	case *ast.MoveExpr:
		return flowExprPerformsCall(e.Operand)
	case *ast.ParenExpr:
		return flowExprPerformsCall(e.Inner)
	}
	return false
}

func identNameOf(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
