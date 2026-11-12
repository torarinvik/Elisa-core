package parser

import (
	"elisacore/src/ast"
	"elisacore/src/machineroots"
	"sort"
)

// collectStmtRootIdents extends collectExprRootIdents into statement-bearing expression
// children (blocks, lambdas, recovery/handler arms, and nested control flow). Those bodies
// execute conditionally but still need their driven-resource roots licensed by the enclosing
// machine.
func collectStmtRootIdents(stmt ast.Stmt, out map[string]bool) {
	machineroots.CollectStmtRootIdents(stmt, out)
}

// exprMentionsIdent reports whether an expression references the named identifier —
// distinguishing `Expr(depth > 1)` (a condition over the payload) from `Expr(1)` /
// `Expr(limit)` (an equality against the payload's scalar local).
func exprMentionsIdent(expr ast.Expr, name string) bool {
	return machineroots.ExprMentionsIdent(expr, name)
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
