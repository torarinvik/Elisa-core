package semantic

import "elisacore/src/ast"

// The unreserved counting-fill lint catches the shape parser auto-reserve cannot rewrite:
// a provably bounded counting loop grows one darray, but the immediately preceding statement is
// not `xs.reserve(bound)`. The fix is cheap and explicit, and under -Wperf this closes the
// "silent repeated reallocations" path when a harmless statement sits between declaration and fill.
func (a *Analyzer) checkUnreservedCountingFills(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.findUnreservedCountingFills(fn.Body)
}

func (a *Analyzer) findUnreservedCountingFills(stmts []ast.Stmt) {
	var prev ast.Stmt
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ForStmt:
			a.flagUnreservedCountingFill(prev, s)
			a.findUnreservedCountingFills(s.Body)
		case *ast.WhileStmt:
			a.findUnreservedCountingFills(s.Body)
		case *ast.IterForStmt:
			a.findUnreservedCountingFills(s.Body)
		case *ast.IfStmt:
			a.findUnreservedCountingFills(s.Then)
			a.findUnreservedCountingFills(s.Else)
		case *ast.ScopeStmt:
			a.findUnreservedCountingFills(s.Body)
		case *ast.CanStmt:
			a.findUnreservedCountingFills(s.Body)
		case *ast.WithStmt:
			a.findUnreservedCountingFills(s.Body)
		case *ast.RegionStmt:
			a.findUnreservedCountingFills(s.Body)
		case *ast.InStoreStmt:
			a.findUnreservedCountingFills(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				a.findUnreservedCountingFills(arm.Body)
			}
		}
		prev = stmt
	}
}

func (a *Analyzer) flagUnreservedCountingFill(prev ast.Stmt, loop *ast.ForStmt) {
	if loop.PreReserve != nil || len(loop.PreReserves) != 0 {
		return
	}
	loopBound, ok := semanticCountingLoopBoundExpr(loop)
	if !ok {
		return
	}
	target := ""
	var perIteration ast.Expr
	growthCounts := collectGrowthTargetCounts(loop.Body)
	for name, growth := range growthCounts {
		if !a.growthTargetIsDArray(loop.Body, name) {
			continue
		}
		if target != "" {
			return
		}
		target = name
		perIteration = growth
	}
	if target == "" {
		return
	}
	expected := loopBound
	if !semanticReserveExprIsIntOne(perIteration) {
		expected = semanticMultiplyReserveExpr(loopBound, perIteration, loop.Pos())
	}
	if stmtIsReserveForBound(prev, target, expected) {
		return
	}
	a.perfLint(loop.Pos(), "counting loop grows %q without a matching immediately preceding reserve; add `%s.reserve(%s)` before the loop or keep the fresh darray declaration adjacent so auto-reserve can synthesize it", target, target, optimizationExprString(expected))
}

func (a *Analyzer) growthTargetIsDArray(body []ast.Stmt, name string) bool {
	found := false
	a.walkStaticStmts(body, func(e ast.Expr) bool {
		if found {
			return false
		}
		call, ok := e.(*ast.CallExpr)
		if !ok || call == nil {
			return false
		}
		field, ok := call.Func.(*ast.FieldExpr)
		if !ok || field == nil || (field.Field != "push" && field.Field != "extend") {
			return false
		}
		recv, ok := field.Object.(*ast.Ident)
		if !ok || recv.Name != name {
			return false
		}
		found = isDArrayTypeMaybeRef(a.exprTypes[field.Object])
		return false
	})
	return found
}

func stmtIsReserveForBound(stmt ast.Stmt, name string, expected ast.Expr) bool {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok || exprStmt == nil {
		return false
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || field.Field != "reserve" {
		return false
	}
	recv, ok := field.Object.(*ast.Ident)
	if !ok || recv.Name != name || len(call.Args) != 1 {
		return false
	}
	return semanticReserveBoundAtLeast(call.Args[0], expected)
}
