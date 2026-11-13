package flowshape

import "elisacore/src/ast"

func VisitAssignments(stmts []ast.Stmt, name string, visit func(ast.Stmt)) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.AssignStmt:
			if identNameOf(n.Target) == name {
				visit(n)
			}
		case *ast.AugAssignStmt:
			if identNameOf(n.Target) == name {
				visit(n)
			}
		case *ast.IfStmt:
			VisitAssignments(n.Then, name, visit)
			for _, elif := range n.Elifs {
				VisitAssignments(elif.Body, name, visit)
			}
			VisitAssignments(n.Else, name, visit)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				VisitAssignments(arm.Body, name, visit)
			}
		case *ast.ScopeStmt:
			VisitAssignments(n.Body, name, visit)
		case *ast.CanStmt:
			VisitAssignments(n.Body, name, visit)
		case *ast.InStoreStmt:
			VisitAssignments(n.Body, name, visit)
		case *ast.RegionStmt:
			VisitAssignments(n.Body, name, visit)
		}
	}
}

// SequentialBranchMutations counts the top-level branch statements (each `if`-chain or
// `match`) that conditionally mutate `name` on a path that FALLS THROUGH to the next sibling.
// Unconditional top-level mutations are not counted — two of those are one straight-line block,
// which R6 permits. Crucially, a guard branch that mutates then diverts (`if c: n <- n+2;
// continue`) does not compound with a later branch — control never reaches both in one iteration —
// so only fall-through mutations, which can actually stack on one path, are tallied.
func SequentialBranchMutations(body []ast.Stmt, name string) int {
	count := 0
	for _, stmt := range body {
		switch n := stmt.(type) {
		case *ast.IfStmt:
			if flowIfHasFallthroughMutation(n, name) {
				count++
			}
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				if flowArmMutatesAndFallsThrough(arm.Body, name) {
					count++
					break
				}
			}
		}
	}
	return count
}

// flowIfHasFallthroughMutation reports whether some arm of an `if`-chain mutates `name` and does
// not divert (so it reaches the next sibling statement). Walks the desugared `elif` chain.
func flowIfHasFallthroughMutation(ifStmt *ast.IfStmt, name string) bool {
	cur := ifStmt
	for {
		if flowArmMutatesAndFallsThrough(cur.Then, name) {
			return true
		}
		for _, elif := range cur.Elifs {
			if flowArmMutatesAndFallsThrough(elif.Body, name) {
				return true
			}
		}
		if next := ElseIfContinuation(cur.Else); next != nil {
			cur = next
			continue
		}
		return flowArmMutatesAndFallsThrough(cur.Else, name)
	}
}

// flowArmMutatesAndFallsThrough reports whether an arm body mutates `name` and does not end in a
// diverting statement (`continue`/`return`/`break`/`raise`) — i.e. control can reach a following
// sibling branch after the mutation, which is the only way two branch mutations compound.
func flowArmMutatesAndFallsThrough(body []ast.Stmt, name string) bool {
	if BlockEndsInExit(body) {
		return false
	}
	found := false
	VisitAssignments(body, name, func(ast.Stmt) { found = true })
	return found
}

// BlockEndsInExit reports whether a block's last statement unconditionally diverts control.
func BlockEndsInExit(body []ast.Stmt) bool {
	if len(body) == 0 {
		return false
	}
	switch n := body[len(body)-1].(type) {
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
		return true
	case *ast.ExprStmt:
		_, isRaise := n.Expr.(*ast.RaiseExpr)
		return isRaise
	}
	return false
}
