package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/flowshape"
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
	if flowshape.HasDirectProgress(info.body, info.carried) {
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
	if !flowshape.StmtsMakeProgress(body) {
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
	return flowshape.StmtsCallOnNames(info.body, names)
}

func flowProgressMessage() string {
	return "flow warning [-Wflow]: this loop branch neither advances a cursor, changes loop state, " +
		"nor exits — the loop can spin forever on this path. Advance the cursor, transition the " +
		"mode, or exit (`return`/`break`/`raise`) on every branch. If the branch is deliberately a " +
		"no-op that another path will make progress from, wrap the loop in " +
		"`can Unsafe.AssumeProgress:`."
}
