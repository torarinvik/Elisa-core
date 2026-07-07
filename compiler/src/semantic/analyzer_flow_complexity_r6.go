package semantic

import (
	"elisacore/src/ast"
)

// R6 — single mutation per binding per path (docs/121 §3-R6). When one loop-carried binding is
// mutated by two SEPARATE sequential branches at the top of the body —
//
//	if opened: depth <- depth + 1
//	if closed: depth <- depth - 1
//
// — the path where both conditions hold mutates it twice, and the result is coupled to the order
// the two branches were written. The clean form computes the whole delta in one place (a `match`
// expression) and applies it once. Two mutations in the SAME straight-line block are fine
// (deliberate); one branch mutating on several of its own arms is fine (only one arm runs). Only
// mutation across ≥ 2 sequential sibling branches is the order-coupled shape.
//
// This is the least-certain rule — an order-coupled update is often correct — so docs/121 §1 pins
// it at warn-tier forever in v1: it emits through flowLintWarnOnly and never fails a strict build.
func (a *Analyzer) checkFlowSingleMutation(info *loopFlowInfo) {
	if info == nil {
		return
	}
	for name := range info.carried {
		if flowSequentialBranchMutations(info.body, name) >= 2 {
			a.flowLintWarnOnly(info.pos, "%s", flowSingleMutationMessage(name))
		}
	}
}

func flowSingleMutationMessage(name string) string {
	return "flow warning [-Wflow]: loop-carried `" + name + "` is mutated by two or more separate " +
		"branches in sequence — on the path where both fire it changes twice, and the outcome is " +
		"coupled to the order the branches were written. Compute the whole delta in one place and " +
		"apply it once:\n" +
		"    " + name + " <- " + name + " + (match …: …)   # one mutation, order-free\n" +
		"  (R6 is advisory — it never fails a build. To silence it, wrap the loop in " +
		"`can ComplexFlow:`.)"
}

// flowSequentialBranchMutations counts the top-level branch statements (each `if`-chain or
// `match`) that conditionally mutate `name` on a path that FALLS THROUGH to the next sibling.
// Unconditional top-level mutations are not counted — two of those are one straight-line block,
// which R6 permits. Crucially, a guard branch that mutates then diverts (`if c: n <- n+2;
// continue`) does not compound with a later branch — control never reaches both in one iteration —
// so only fall-through mutations, which can actually stack on one path, are tallied.
func flowSequentialBranchMutations(body []ast.Stmt, name string) int {
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
		if next := flowElseIfContinuation(cur.Else); next != nil {
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
	if flowBlockEndsInExit(body) {
		return false
	}
	found := false
	visitAssignments(body, name, func(ast.Stmt) { found = true })
	return found
}

// flowBlockEndsInExit reports whether a block's last statement unconditionally diverts control.
func flowBlockEndsInExit(body []ast.Stmt) bool {
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
