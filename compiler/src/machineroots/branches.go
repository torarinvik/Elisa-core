package machineroots

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// A local in a branch that terminates never scopes over the copied continuation.
// Reject only locals whose branch can fall through, where continuation duplication
// could accidentally resolve a name to that branch-local binding.
func HasFallthroughLocalDecl(stmt ast.Stmt, markers map[lexer.Pos]string) bool {
	n, ok := stmt.(*ast.IfStmt)
	if !ok {
		return false
	}
	if CanFallThrough(n.Then, markers) && hasLocalDeclInSequence(n.Then) {
		return true
	}
	if len(n.Else) > 0 && CanFallThrough(n.Else, markers) && hasLocalDeclInSequence(n.Else) {
		return true
	}
	for _, clause := range n.Elifs {
		if CanFallThrough(clause.Body, markers) && hasLocalDeclInSequence(clause.Body) {
			return true
		}
		if hasFallthroughLocalDeclInSequence(clause.Body, markers) {
			return true
		}
	}
	return hasFallthroughLocalDeclInSequence(n.Then, markers) || hasFallthroughLocalDeclInSequence(n.Else, markers)
}

func hasFallthroughLocalDeclInSequence(stmts []ast.Stmt, markers map[lexer.Pos]string) bool {
	for _, stmt := range stmts {
		if n, ok := stmt.(*ast.IfStmt); ok && HasFallthroughLocalDecl(n, markers) {
			return true
		}
	}
	return false
}

func hasLocalDeclInSequence(stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		if hasLocalDecl(stmt) {
			return true
		}
	}
	return false
}

func hasLocalDecl(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt, *ast.LetDestructureStmt, *ast.TupleBindStmt, *ast.MoveBindStmt:
		return true
	case *ast.IfStmt:
		for _, child := range n.Then {
			if hasLocalDecl(child) {
				return true
			}
		}
		for _, child := range n.Else {
			if hasLocalDecl(child) {
				return true
			}
		}
		for _, clause := range n.Elifs {
			for _, child := range clause.Body {
				if hasLocalDecl(child) {
					return true
				}
			}
		}
	case *ast.CanStmt:
		for _, child := range n.Body {
			if hasLocalDecl(child) {
				return true
			}
		}
	}
	return false
}

func CanFallThrough(stmts []ast.Stmt, markers map[lexer.Pos]string) bool {
	canFall := true
	for _, stmt := range stmts {
		if !canFall {
			return false
		}
		switch n := stmt.(type) {
		case *ast.BreakStmt:
			canFall = false
		case *ast.ReturnStmt:
			canFall = false
		case *ast.IfStmt:
			thenFalls := CanFallThrough(n.Then, markers)
			elseFalls := len(n.Else) == 0 || CanFallThrough(n.Else, markers)
			for _, clause := range n.Elifs {
				thenFalls = thenFalls || CanFallThrough(clause.Body, markers)
			}
			canFall = thenFalls || elseFalls
		}
	}
	return canFall
}

func HasBranchMarker(stmt *ast.IfStmt, markers map[lexer.Pos]string) bool {
	if len(markers) == 0 {
		return false
	}
	var scan func([]ast.Stmt) bool
	scan = func(stmts []ast.Stmt) bool {
		for _, stmt := range stmts {
			switch n := stmt.(type) {
			case *ast.BreakStmt:
				if _, ok := markers[n.Position]; ok {
					return true
				}
			case *ast.IfStmt:
				if scan(n.Then) || scan(n.Else) {
					return true
				}
				for _, clause := range n.Elifs {
					if scan(clause.Body) {
						return true
					}
				}
			}
		}
		return false
	}
	if scan(stmt.Then) || scan(stmt.Else) {
		return true
	}
	for _, clause := range stmt.Elifs {
		if scan(clause.Body) {
			return true
		}
	}
	return false
}
