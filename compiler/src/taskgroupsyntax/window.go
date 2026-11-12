package taskgroupsyntax

import "elisacore/src/ast"

func WindowStmtSafe(stmt ast.Stmt, groupName string, walk func(any, func(any)), submitCall func(*ast.CallExpr)) bool {
	switch n := stmt.(type) {
	case *ast.ForStmt:
		if n == nil || n.Name == groupName || IsAPIName(n.Name) || n.Name == "pool_submit1" || n.PreReserve != nil || len(n.PreReserves) != 0 {
			return false
		}
		for _, expr := range []ast.Expr{n.Start, n.End, n.Step} {
			facts := CollectFacts(expr, groupName, walk)
			if facts.GroupRef || facts.GroupAPI || facts.UntrustedCall || facts.SafeSubmitCount != 0 || facts.EarlyExit {
				return false
			}
		}
		for _, child := range n.Body {
			if !WindowStmtSafe(child, groupName, walk, submitCall) {
				return false
			}
		}
		return true
	case *ast.CanStmt:
		if n == nil || n.As != "" || n.HandlerName != "" || len(n.HandlerArgs) != 0 {
			return false
		}
		facts := CollectFacts(n.Permissions, groupName, walk)
		if facts.GroupRef || facts.GroupAPI || facts.UntrustedCall || facts.SafeSubmitCount != 0 || facts.EarlyExit {
			return false
		}
		for _, child := range n.Body {
			if !WindowStmtSafe(child, groupName, walk, submitCall) {
				return false
			}
		}
		return true
	case *ast.ExprStmt:
		if n == nil {
			return false
		}
		call, ok := n.Expr.(*ast.CallExpr)
		if ok && call != nil && callName(call) == "task_group_add" && len(call.Args) == 2 && groupRefMatches(call.Args[0], groupName) {
			submit, isSubmit := call.Args[1].(*ast.CallExpr)
			if !isSubmit || submit == nil || !submit.SafeConcurrencySugar || callName(submit) != "pool_submit1" || len(submit.Args) != 3 {
				return false
			}
			facts := CollectFacts(submit.Args, groupName, walk)
			if facts.GroupRef || facts.GroupAPI || facts.UntrustedCall || facts.SafeSubmitCount != 0 || facts.EarlyExit || facts.GroupShadow || facts.ApiShadow {
				return false
			}
			submitCall(submit)
			return true
		}
		facts := CollectFacts(stmt, groupName, walk)
		return !facts.GroupRef && !facts.GroupAPI && !facts.UntrustedCall && facts.SafeSubmitCount == 0 && !facts.EarlyExit && !facts.GroupShadow && !facts.ApiShadow
	default:
		facts := CollectFacts(stmt, groupName, walk)
		return !facts.GroupRef && !facts.GroupAPI && !facts.UntrustedCall && facts.SafeSubmitCount == 0 && !facts.EarlyExit && !facts.GroupShadow && !facts.ApiShadow
	}
}

func groupRefMatches(expr ast.Expr, groupName string) bool {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.AddrOfExpr:
			ident, ok := n.Operand.(*ast.Ident)
			return ok && ident != nil && ident.Name == groupName
		default:
			return false
		}
	}
	return false
}
