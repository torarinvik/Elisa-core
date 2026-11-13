package staticwalk

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func BlockAlwaysTerminates(stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		if staticStmtAlwaysTerminates(stmt) {
			return true
		}
	}
	return false
}

func BlockTerminationIssue(stmts []ast.Stmt) (lexer.Pos, string, bool) {
	if len(stmts) == 0 {
		return lexer.Pos{}, "the function body can fall through", true
	}
	for _, stmt := range stmts {
		if staticStmtAlwaysTerminates(stmt) {
			return lexer.Pos{}, "", false
		}
		if pos, detail, ok := staticStmtTerminationIssue(stmt); ok {
			return pos, detail, true
		}
	}
	last := stmts[len(stmts)-1]
	return last.Pos(), "control can fall through after this statement", true
}

func staticStmtAlwaysTerminates(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt, *ast.StaticErrorStmt:
		return true
	case *ast.StaticBlockStmt:
		return BlockAlwaysTerminates(n.Body)
	case *ast.IfStmt:
		return staticIfStmtAlwaysTerminates(n.Then, n.Elifs, n.Else)
	case *ast.StaticIfStmt:
		return staticStaticIfStmtAlwaysTerminates(n.Then, n.Elifs, n.Else)
	case *ast.MatchStmt:
		return staticMatchStmtAlwaysTerminates(n)
	default:
		return false
	}
}

func staticStmtTerminationIssue(stmt ast.Stmt) (lexer.Pos, string, bool) {
	switch n := stmt.(type) {
	case *ast.IfStmt:
		if len(n.Else) == 0 {
			return n.Pos(), "this if statement has no else branch", true
		}
		if !BlockAlwaysTerminates(n.Then) {
			if pos, detail, ok := BlockTerminationIssue(n.Then); ok {
				return pos, "the then branch does not terminate: " + detail, true
			}
			return n.Pos(), "the then branch does not terminate", true
		}
		for _, elif := range n.Elifs {
			if !BlockAlwaysTerminates(elif.Body) {
				if pos, detail, ok := BlockTerminationIssue(elif.Body); ok {
					return pos, "an elif branch does not terminate: " + detail, true
				}
				return elif.Position, "an elif branch does not terminate", true
			}
		}
		if !BlockAlwaysTerminates(n.Else) {
			if pos, detail, ok := BlockTerminationIssue(n.Else); ok {
				return pos, "the else branch does not terminate: " + detail, true
			}
			return n.Pos(), "the else branch does not terminate", true
		}
	case *ast.StaticIfStmt:
		if len(n.Else) == 0 {
			return n.Pos(), "this static if statement has no else branch", true
		}
		if !BlockAlwaysTerminates(n.Then) {
			if pos, detail, ok := BlockTerminationIssue(n.Then); ok {
				return pos, "the then branch does not terminate: " + detail, true
			}
			return n.Pos(), "the then branch does not terminate", true
		}
		for _, elif := range n.Elifs {
			if !BlockAlwaysTerminates(elif.Body) {
				if pos, detail, ok := BlockTerminationIssue(elif.Body); ok {
					return pos, "a static elif branch does not terminate: " + detail, true
				}
				return elif.Position, "a static elif branch does not terminate", true
			}
		}
		if !BlockAlwaysTerminates(n.Else) {
			if pos, detail, ok := BlockTerminationIssue(n.Else); ok {
				return pos, "the else branch does not terminate: " + detail, true
			}
			return n.Pos(), "the else branch does not terminate", true
		}
	case *ast.MatchStmt:
		hasCatchAll := false
		for _, arm := range n.Arms {
			switch arm.Pattern.(type) {
			case *ast.MatchWildcardPattern, *ast.MatchBindPattern:
				hasCatchAll = true
			}
			if !BlockAlwaysTerminates(arm.Body) {
				if pos, detail, ok := BlockTerminationIssue(arm.Body); ok {
					return pos, "a match arm does not terminate: " + detail, true
				}
				return arm.Position, "a match arm does not terminate", true
			}
		}
		if !hasCatchAll {
			return n.Pos(), "this match statement has no catch-all arm", true
		}
	case *ast.StaticBlockStmt:
		return BlockTerminationIssue(n.Body)
	}
	return stmt.Pos(), "this statement can fall through", true
}

func staticMatchStmtAlwaysTerminates(stmt *ast.MatchStmt) bool {
	if stmt == nil || len(stmt.Arms) == 0 {
		return false
	}
	hasWildcard := false
	for _, arm := range stmt.Arms {
		switch arm.Pattern.(type) {
		case *ast.MatchWildcardPattern, *ast.MatchBindPattern:
			// docs/122 §5.1: a guarded catch-all can fail, so it does not guarantee
			// the match terminates.
			if arm.Guard == nil {
				hasWildcard = true
			}
		}
		if !BlockAlwaysTerminates(arm.Body) {
			return false
		}
	}
	return hasWildcard
}

func staticIfStmtAlwaysTerminates(then []ast.Stmt, elifs []ast.ElifClause, elseStmts []ast.Stmt) bool {
	if len(elseStmts) == 0 {
		return false
	}
	if !BlockAlwaysTerminates(then) || !BlockAlwaysTerminates(elseStmts) {
		return false
	}
	for _, elif := range elifs {
		if !BlockAlwaysTerminates(elif.Body) {
			return false
		}
	}
	return true
}

func staticStaticIfStmtAlwaysTerminates(then []ast.Stmt, elifs []ast.StaticElifClause, elseStmts []ast.Stmt) bool {
	if len(elseStmts) == 0 {
		return false
	}
	if !BlockAlwaysTerminates(then) || !BlockAlwaysTerminates(elseStmts) {
		return false
	}
	for _, elif := range elifs {
		if !BlockAlwaysTerminates(elif.Body) {
			return false
		}
	}
	return true
}
