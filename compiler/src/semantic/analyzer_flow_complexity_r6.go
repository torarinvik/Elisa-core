package semantic

import (
	"elisacore/src/flowshape"
	"maps"
	"slices"
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
	for _, name := range slices.Sorted(maps.Keys(info.carried)) { // map: sort for a stable report order
		if flowshape.SequentialBranchMutations(info.body, name) >= 2 {
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
