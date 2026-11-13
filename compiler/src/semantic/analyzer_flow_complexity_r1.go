package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/flowshape"
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
	if flowshape.BlockDepth(info.body) > flowMaxNestingDepth {
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

// flowElseIfContinuation shares the parser's desugared elif shape with the mutation rule.
func flowElseIfContinuation(body []ast.Stmt) *ast.IfStmt {
	return flowshape.ElseIfContinuation(body)
}
