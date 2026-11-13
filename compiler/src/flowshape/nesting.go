package flowshape

import "elisacore/src/ast"

// BlockDepth returns the maximum conditional nesting depth reached inside a plain block.
// An `if`-chain or a `match` contributes one level plus the depth of its deepest body; block
// wrappers (scope/can/…) are transparent; nested loops reset to zero.
func BlockDepth(stmts []ast.Stmt) int {
	max := 0
	for _, stmt := range stmts {
		if d := flowStmtDepth(stmt); d > max {
			max = d
		}
	}
	return max
}

func flowStmtDepth(stmt ast.Stmt) int {
	switch n := stmt.(type) {
	case *ast.IfStmt:
		// The scoped-loop-header desugar wraps a `while … |caps|:` / value-form loop in a synthetic
		// `if true: [decls, loop]` (parser/loop_header.go:164). That wrapper is loop scaffolding,
		// not a real branch, so it is TRANSPARENT — otherwise every scoped inner loop adds a phantom
		// level and stage1 (all scoped loops) trips R1 on desugar artifacts, not real nesting.
		if flowIsConstTrueGuard(n) {
			return maxInt(BlockDepth(n.Then), BlockDepth(n.Else))
		}
		// An `if`/`elif`/`else` chain is ONE nesting level. The runtime parser desugars `elif`
		// into a nested `else: if …` (parser …parsesingleexprblockvalue.go:185) rather than
		// filling the flat Elifs slice, so the chain must be walked and collapsed here — otherwise
		// a flat N-way dispatch reads as depth N and every real branchy loop trips R1. Only genuine
		// nesting INSIDE a clause body (an if within a then/else block) adds a level.
		inner := 0
		cur := n
		for {
			if d := BlockDepth(cur.Then); d > inner {
				inner = d
			}
			for _, elif := range cur.Elifs {
				if d := BlockDepth(elif.Body); d > inner {
					inner = d
				}
			}
			if next := ElseIfContinuation(cur.Else); next != nil {
				cur = next
				continue // same chain, not a new level
			}
			if d := BlockDepth(cur.Else); d > inner {
				inner = d
			}
			break
		}
		return 1 + inner
	case *ast.MatchStmt:
		// A match is transparent — structured dispatch, not nesting (see the rule comment). It
		// contributes the deepest if-nesting among its arms, but adds no level of its own.
		inner := 0
		for _, arm := range n.Arms {
			if d := flowMatchArmDepth(arm.Body); d > inner {
				inner = d
			}
		}
		return inner
	case *ast.ScopeStmt:
		return BlockDepth(n.Body)
	case *ast.CanStmt:
		return BlockDepth(n.Body)
	case *ast.InStoreStmt:
		return BlockDepth(n.Body)
	case *ast.RegionStmt:
		return BlockDepth(n.Body)
	}
	// Nested loops (While/For/IterFor) and plain statements do not add conditional depth.
	return 0
}

// flowIsConstTrueGuard reports whether an `if` is the `if true:` scoped-loop-header wrapper (a
// literal-true condition) — scaffolding the depth counter must see through.
func flowIsConstTrueGuard(ifStmt *ast.IfStmt) bool {
	lit, ok := ifStmt.Cond.(*ast.BoolLit)
	return ok && lit.Value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ElseIfContinuation returns the inner `if` when an `else` block is exactly one `if` — the
// shape the parser produces for `elif` (a desugared `else: if …`). Such an `else` continues the
// same dispatch chain and must not be counted as a new nesting level. A multi-statement `else`, or
// one whose single statement is not an `if`, is a genuine nested block and returns nil.
func ElseIfContinuation(elseBlock []ast.Stmt) *ast.IfStmt {
	if len(elseBlock) != 1 {
		return nil
	}
	inner, _ := elseBlock[0].(*ast.IfStmt)
	return inner
}

// flowMatchArmDepth is BlockDepth for a match arm, with the trailing-guard carve-out: a
// single `if`/`if-else` as the arm's last statement, whose branches contain no further
// conditionals, contributes zero rather than one. Any other position (or a guard with nested
// conditionals) counts normally.
func flowMatchArmDepth(stmts []ast.Stmt) int {
	if len(stmts) == 0 {
		return 0
	}
	max := 0
	for i, stmt := range stmts {
		isLast := i == len(stmts)-1
		if isLast && flowIsSimpleTrailingGuard(stmt) {
			continue // the carve-out — a clean arm's terminal guard
		}
		if d := flowStmtDepth(stmt); d > max {
			max = d
		}
	}
	return max
}

// flowIsSimpleTrailingGuard reports whether a statement is an `if` whose then/elif/else bodies
// contain no further conditionals — the one guard a clean match arm is allowed to end on for
// free.
func flowIsSimpleTrailingGuard(stmt ast.Stmt) bool {
	ifStmt, ok := stmt.(*ast.IfStmt)
	if !ok {
		return false
	}
	if BlockDepth(ifStmt.Then) > 0 {
		return false
	}
	for _, elif := range ifStmt.Elifs {
		if BlockDepth(elif.Body) > 0 {
			return false
		}
	}
	return BlockDepth(ifStmt.Else) == 0
}
