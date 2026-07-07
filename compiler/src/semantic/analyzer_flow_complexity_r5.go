package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// R5 — per-path progress (docs/121 §3-R5). A `while` body that has a reachable path doing nothing
// — a `pass` arm, an empty `else`, a branch that only reads — can spin forever on that path. The
// existing progress-safety system proves progress for the loop as a whole; this rule sharpens it
// to the branch, pointing at the exact arm that neither advances, changes state, nor exits.
//
// It runs on `while` only (`for`/iter loops are structurally bounded). To stay quiet on the huge
// space of legitimate loops, "progress" is read generously: ANY assignment, ANY call, or ANY exit
// (`return`/`break`/`continue`/`raise`) counts. A call is trusted to advance because it may mutate
// a cursor through a reference — the interprocedural ensure-summary refinement (docs/121 §3-R5
// Change 2) would tighten this, but for the warn-tier rollout the conservative reading catches the
// obvious hang without firing on effectful-call loops (docs/121 §7). The only thing flagged is a
// branch arm that is genuinely inert: no assignment, no call, no exit, anywhere within it.
func (a *Analyzer) checkFlowProgress(info *loopFlowInfo) {
	if info == nil || info.kind != loopWhile {
		return
	}
	// Threaded-cursor loops are exempt (docs/121 §4c). The docs/120 §6 thread-slot desugar ERASES
	// the assignment of a threaded rebind (`parser, _ <- parser.expression()`): the mutating call
	// executes in place, no assignment statement remains, and the erasure can relocate the advance
	// out of the branch it was written in (into a hoisted value block). So the structural
	// per-branch check cannot see that progress and would false-positive on clean threaded parser
	// loops. The reliable, erasure-proof marker of such a loop is that a captured/carried binding is
	// the RECEIVER of a call somewhere in the body — every thread-slot advance is a call on the
	// threaded object. Where that holds, termination is the progress-safety prover's job (consuming
	// docs/118 ensure summaries — the R5 spec's intended Change 2), not this structural sharpening.
	if flowLoopCallsOnCarried(info) {
		return
	}
	// An unconditional top-level progress statement means every iteration advances regardless of
	// which branch is taken — nothing to flag. A plain local read (`c = source[i].char()`) is NOT
	// that: it produces a value but does not advance, so it must not silence the per-branch check.
	if flowHasDirectProgress(info.body, info.carried) {
		return
	}
	for _, stmt := range info.body {
		switch n := stmt.(type) {
		case *ast.IfStmt:
			a.flowReportInertArm(n.Then, n.Position)
			for _, elif := range n.Elifs {
				a.flowReportInertArm(elif.Body, elif.Position)
			}
			if len(n.Else) > 0 {
				a.flowReportInertArm(n.Else, n.Position)
			}
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				a.flowReportInertArm(arm.Body, arm.Position)
			}
		}
	}
}

// flowReportInertArm emits R5 for a branch arm that makes no progress on any path through it.
func (a *Analyzer) flowReportInertArm(body []ast.Stmt, pos lexer.Pos) {
	if !flowStmtsMakeProgress(body) {
		a.flowLint(pos, "%s", flowProgressMessage())
	}
}

// flowLoopCallsOnCarried reports whether any carried or captured binding is used as a call
// receiver anywhere in the loop body — the erasure-proof marker of a thread-slot loop (see the
// call site). Walks statements and their expressions, but not into nested loops.
func flowLoopCallsOnCarried(info *loopFlowInfo) bool {
	names := map[string]bool{}
	for name := range info.carried {
		names[name] = true
	}
	for _, name := range info.captures {
		names[name] = true
	}
	if len(names) == 0 {
		return false
	}
	return flowStmtsCallOnNames(info.body, names)
}

func flowStmtsCallOnNames(stmts []ast.Stmt, names map[string]bool) bool {
	for _, stmt := range stmts {
		if flowStmtCallOnNames(stmt, names) {
			return true
		}
	}
	return false
}

func flowStmtCallOnNames(stmt ast.Stmt, names map[string]bool) bool {
	switch n := stmt.(type) {
	case *ast.ExprStmt:
		return flowExprCallOnNames(n.Expr, names)
	case *ast.AssignStmt:
		return flowExprCallOnNames(n.Value, names) || flowExprCallOnNames(n.Target, names)
	case *ast.AugAssignStmt:
		return flowExprCallOnNames(n.Value, names)
	case *ast.VarDeclStmt:
		return flowExprCallOnNames(n.Value, names)
	case *ast.TupleBindStmt:
		return flowExprCallOnNames(n.Value, names)
	case *ast.ReturnStmt:
		return flowExprCallOnNames(n.Value, names)
	case *ast.IfStmt:
		if flowExprCallOnNames(n.Cond, names) || flowStmtsCallOnNames(n.Then, names) || flowStmtsCallOnNames(n.Else, names) {
			return true
		}
		for _, elif := range n.Elifs {
			if flowExprCallOnNames(elif.Cond, names) || flowStmtsCallOnNames(elif.Body, names) {
				return true
			}
		}
	case *ast.MatchStmt:
		if flowExprCallOnNames(n.Value, names) {
			return true
		}
		for _, arm := range n.Arms {
			if flowStmtsCallOnNames(arm.Body, names) {
				return true
			}
		}
	case *ast.ScopeStmt:
		return flowStmtsCallOnNames(n.Body, names)
	case *ast.CanStmt:
		return flowStmtsCallOnNames(n.Body, names)
	case *ast.InStoreStmt:
		return flowStmtsCallOnNames(n.Body, names)
	case *ast.RegionStmt:
		return flowStmtsCallOnNames(n.Body, names)
	}
	return false
}

// flowExprCallOnNames reports whether the expression contains a call whose receiver root is one of
// the names, or (conservatively) an ExprBlock in whose statements such a call hides — the value
// block a thread-slot advance is hoisted into.
func flowExprCallOnNames(expr ast.Expr, names map[string]bool) bool {
	switch e := expr.(type) {
	case nil:
		return false
	case *ast.CallExpr:
		if _, receiver := callNameAndReceiver(e); receiver != "" && names[receiver] {
			return true
		}
		if flowExprCallOnNames(e.Func, names) || flowExprCallOnNames(e.SafeReceiver, names) {
			return true
		}
		for _, arg := range e.Args {
			if flowExprCallOnNames(arg, names) {
				return true
			}
		}
	case *ast.FieldExpr:
		return flowExprCallOnNames(e.Object, names)
	case *ast.BinaryExpr:
		return flowExprCallOnNames(e.Left, names) || flowExprCallOnNames(e.Right, names)
	case *ast.UnaryExpr:
		return flowExprCallOnNames(e.Operand, names)
	case *ast.ParenExpr:
		return flowExprCallOnNames(e.Inner, names)
	case *ast.MoveExpr:
		return flowExprCallOnNames(e.Operand, names)
	case *ast.ExprBlock:
		return flowStmtsCallOnNames(e.Stmts, names) || flowExprCallOnNames(e.Value, names)
	}
	return false
}

func flowProgressMessage() string {
	return "flow warning [-Wflow]: this loop branch neither advances a cursor, changes loop state, " +
		"nor exits — the loop can spin forever on this path. Advance the cursor, transition the " +
		"mode, or exit (`return`/`break`/`raise`) on every branch. If the branch is deliberately a " +
		"no-op that another path will make progress from, wrap the loop in " +
		"`can Unsafe.AssumeProgress:`."
}

// flowHasDirectProgress reports whether any DIRECT (top-level, non-branch) statement of the loop
// body is unconditional progress — a carried-binding mutation, an exit, or a bare mutating call —
// which runs every iteration and so guarantees the loop advances regardless of its branches. A
// value-producing read declaration (`c: char = source[i].char()`) is deliberately NOT progress:
// it is the scanner's per-iteration lookahead, not an advance.
func flowHasDirectProgress(stmts []ast.Stmt, carried map[string]bool) bool {
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

// flowStmtsMakeProgress reports whether a block can make progress on some path within it: it holds
// a progress statement directly, or a branch whose some arm makes progress. Descends through
// branch/scope wrappers but not into nested loops (reaching one at all is itself an effect, so a
// nested loop counts as progress).
func flowStmtsMakeProgress(stmts []ast.Stmt) bool {
	for _, stmt := range stmts {
		if flowIsProgressStmt(stmt) {
			return true
		}
		switch n := stmt.(type) {
		case *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt:
			return true // running an inner loop is itself progress
		case *ast.IfStmt:
			if flowStmtsMakeProgress(n.Then) {
				return true
			}
			for _, elif := range n.Elifs {
				if flowStmtsMakeProgress(elif.Body) {
					return true
				}
			}
			if flowStmtsMakeProgress(n.Else) {
				return true
			}
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				if flowStmtsMakeProgress(arm.Body) {
					return true
				}
			}
		case *ast.ScopeStmt:
			if flowStmtsMakeProgress(n.Body) {
				return true
			}
		case *ast.CanStmt:
			if flowStmtsMakeProgress(n.Body) {
				return true
			}
		case *ast.InStoreStmt:
			if flowStmtsMakeProgress(n.Body) {
				return true
			}
		case *ast.RegionStmt:
			if flowStmtsMakeProgress(n.Body) {
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
