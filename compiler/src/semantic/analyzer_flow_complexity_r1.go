package semantic

import (
	"elisacore/src/ast"
)

// R1 — nesting budget (docs/121 §3-R1). Deeply STACKED `if`/`elif` chains inside a loop body are
// the shape of a decision tree that wants to be a `match` on a named state (or an extracted
// helper). Each `if`/`elif` chain counts as ONE level regardless of elif count. A nested loop
// resets the counter — it is its own jurisdiction, analyzed as its own loop.
//
// A `match` is TRANSPARENT to the counter: it adds no level. This is the load-bearing calibration
// result (docs/121 §4c). A `match` on a value is structured typed dispatch — the exact clean shape
// R2/R3 steer toward — not a decision tree hiding a state machine. Counting it as nesting fired R1
// on the gold-standard `read_fstring` rewrite itself (`match mode → match character → guard if`),
// the same contradiction Phase A found in R3. So only `if`-nesting accrues budget; `match` arms
// contribute their own internal `if`-depth but the `match` itself is free.
//
// Carve-out: inside a `match` arm, one trailing `if`/`if-else` whose branches hold no further
// conditionals does NOT add a level — a clean arm ending in a single guard `if at_boundary: mode
// <- Next` is not nesting.
//
// Suppressed when R2 fired on the same loop: R2's enum-state rewrite collapses the nesting, so
// emitting R1 too would be two diagnostics prescribing one fix (docs/121 §7).
func (a *Analyzer) checkFlowNestingBudget(info *loopFlowInfo) {
	if info == nil || info.r2Fired {
		return
	}
	if flowBlockDepth(info.body) > flowMaxNestingDepth {
		a.flowLint(info.pos, "%s", flowNestingMessage())
	}
}

// flowMaxNestingDepth is the deepest `if`-nesting a loop body may reach before R1 fires. Set to 3
// by calibration (docs/121 §4c): stage1 — hand-written clean-reference code — routinely nests
// pattern-guard `if`s three deep (`if x is Ident: … elif op: if type != "": …`), which reads fine.
// Genuine arrow code that hides a state machine goes deeper, so the budget is > 3, not > 2.
const flowMaxNestingDepth = 3

func flowNestingMessage() string {
	return "flow warning [-Wflow]: conditional nesting deeper than 3 levels in this loop body — " +
		"a decision tree hiding a state machine. Either name the states and dispatch once " +
		"(`match mode:` with a single guard per arm), or extract the inner decision into a named " +
		"function so each level reads as one step:\n" +
		"    fn classify(…) -> Mode: …        # the inner if-tree, named\n" +
		"    while COND |…, mode: Mode = …|:\n" +
		"        match mode: …                # one level, one dispatch\n" +
		"  To keep the nesting, wrap the loop in `can ComplexFlow:`."
}

// flowBlockDepth returns the maximum conditional nesting depth reached inside a plain block.
// An `if`-chain or a `match` contributes one level plus the depth of its deepest body; block
// wrappers (scope/can/…) are transparent; nested loops reset to zero.
func flowBlockDepth(stmts []ast.Stmt) int {
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
			return maxInt(flowBlockDepth(n.Then), flowBlockDepth(n.Else))
		}
		// An `if`/`elif`/`else` chain is ONE nesting level. The runtime parser desugars `elif`
		// into a nested `else: if …` (parser …parsesingleexprblockvalue.go:185) rather than
		// filling the flat Elifs slice, so the chain must be walked and collapsed here — otherwise
		// a flat N-way dispatch reads as depth N and every real branchy loop trips R1. Only genuine
		// nesting INSIDE a clause body (an if within a then/else block) adds a level.
		inner := 0
		cur := n
		for {
			if d := flowBlockDepth(cur.Then); d > inner {
				inner = d
			}
			for _, elif := range cur.Elifs {
				if d := flowBlockDepth(elif.Body); d > inner {
					inner = d
				}
			}
			if next := flowElseIfContinuation(cur.Else); next != nil {
				cur = next
				continue // same chain, not a new level
			}
			if d := flowBlockDepth(cur.Else); d > inner {
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
		return flowBlockDepth(n.Body)
	case *ast.CanStmt:
		return flowBlockDepth(n.Body)
	case *ast.InStoreStmt:
		return flowBlockDepth(n.Body)
	case *ast.RegionStmt:
		return flowBlockDepth(n.Body)
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

// flowElseIfContinuation returns the inner `if` when an `else` block is exactly one `if` — the
// shape the parser produces for `elif` (a desugared `else: if …`). Such an `else` continues the
// same dispatch chain and must not be counted as a new nesting level. A multi-statement `else`, or
// one whose single statement is not an `if`, is a genuine nested block and returns nil.
func flowElseIfContinuation(elseBlock []ast.Stmt) *ast.IfStmt {
	if len(elseBlock) != 1 {
		return nil
	}
	inner, _ := elseBlock[0].(*ast.IfStmt)
	return inner
}

// flowMatchArmDepth is flowBlockDepth for a match arm, with the trailing-guard carve-out: a
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
	if flowBlockDepth(ifStmt.Then) > 0 {
		return false
	}
	for _, elif := range ifStmt.Elifs {
		if flowBlockDepth(elif.Body) > 0 {
			return false
		}
	}
	return flowBlockDepth(ifStmt.Else) == 0
}
