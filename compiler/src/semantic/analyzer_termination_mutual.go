package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) collectDirectFunctionCalls(fn *ast.FuncDecl) []*ast.CallExpr {
	var calls []*ast.CallExpr
	if fn == nil {
		return calls
	}
	a.walkStaticStmts(fn.Body, func(expr ast.Expr) bool {
		call, ok := expr.(*ast.CallExpr)
		if !ok || call == nil {
			return false
		}
		if _, ok := a.resolveDirectCallFuncDecl(call); ok {
			calls = append(calls, call)
		}
		return false
	})
	return calls
}

func (a *Analyzer) collectRecursiveSCCEdges(root *ast.FuncDecl) []recursionEdge {
	if root == nil {
		return nil
	}
	visited := map[*ast.FuncDecl]bool{}
	var order []*ast.FuncDecl
	var walk func(*ast.FuncDecl)
	walk = func(fn *ast.FuncDecl) {
		if fn == nil || visited[fn] {
			return
		}
		visited[fn] = true
		order = append(order, fn)
		for _, call := range a.collectDirectFunctionCalls(fn) {
			callee, ok := a.resolveDirectCallFuncDecl(call)
			if !ok || callee == nil {
				continue
			}
			walk(callee)
		}
	}
	walk(root)
	if len(order) == 0 {
		return nil
	}
	reachesRoot := map[*ast.FuncDecl]bool{}
	var reaches func(*ast.FuncDecl, map[*ast.FuncDecl]bool) bool
	reaches = func(fn *ast.FuncDecl, seen map[*ast.FuncDecl]bool) bool {
		if fn == root {
			return true
		}
		if seen[fn] {
			return false
		}
		seen[fn] = true
		for _, call := range a.collectDirectFunctionCalls(fn) {
			callee, ok := a.resolveDirectCallFuncDecl(call)
			if ok && callee != nil && visited[callee] && reaches(callee, seen) {
				return true
			}
		}
		return false
	}
	for _, fn := range order {
		if reaches(fn, map[*ast.FuncDecl]bool{}) {
			reachesRoot[fn] = true
		}
	}
	var edges []recursionEdge
	for _, caller := range order {
		if !reachesRoot[caller] {
			continue
		}
		for _, call := range a.collectDirectFunctionCalls(caller) {
			callee, ok := a.resolveDirectCallFuncDecl(call)
			if ok && reachesRoot[callee] {
				edges = append(edges, recursionEdge{Caller: caller, Callee: callee, Call: call})
			}
		}
	}
	return edges
}

func (a *Analyzer) mutualRecursionVerified(root *ast.FuncDecl, edges []recursionEdge) bool {
	if root == nil || root != a.currentFuncDecl || len(edges) == 0 {
		return false
	}
	members := map[*ast.FuncDecl]bool{}
	for _, edge := range edges {
		members[edge.Caller] = true
		members[edge.Callee] = true
	}
	for member := range members {
		if member.DecreasesWild != "" || len(member.Decreases) == 0 {
			return false
		}
	}
	for _, edge := range edges {
		if _, ok := a.recursiveCallCertificate(edge.Caller, edge.Callee, edge.Call); !ok {
			return false
		}
	}
	return true
}

func (a *Analyzer) crossFunctionMeasureDecreases(caller, callee *ast.FuncDecl, call *ast.CallExpr) bool {
	if caller == nil || callee == nil || call == nil || len(caller.Decreases) == 0 || len(callee.Decreases) == 0 {
		return false
	}
	callerMeasures := decreaseMeasureComponents(caller.Decreases)
	calleeMeasures := decreaseMeasureComponents(callee.Decreases)
	if len(callerMeasures) == 0 || len(callerMeasures) != len(calleeMeasures) {
		return false
	}
	subst := map[string]ast.Expr{}
	args := proofCallArgs(call)
	for i, param := range callee.Params {
		if i < len(args) && args[i] != nil {
			subst[param.Name] = args[i]
		}
	}
	for k := range callerMeasures {
		earlierUnchanged := true
		for j := 0; j < k; j++ {
			if !a.crossMeasureDiffIsZero(callerMeasures[j], calleeMeasures[j], subst) {
				earlierUnchanged = false
				break
			}
		}
		if !earlierUnchanged {
			continue
		}
		if syntacticCrossMeasureDecreases(caller, callee, call, k) && a.syntacticCrossMeasureBounded(caller, k) {
			return true
		}
		if a.crossMeasureStrictlyDecreases(callerMeasures[k], calleeMeasures[k], subst) && a.measureBoundedBelow(callerMeasures[k]) {
			return true
		}
	}
	return false
}

func (a *Analyzer) syntacticCrossMeasureBounded(caller *ast.FuncDecl, k int) bool {
	if caller == nil {
		return false
	}
	measures := decreaseMeasureComponents(caller.Decreases)
	if k >= len(measures) {
		return false
	}
	id, ok := measures[k].(*ast.Ident)
	if !ok || id == nil {
		return false
	}
	for i, param := range caller.Params {
		if param.Name == id.Name && i < len(caller.Params) {
			if a.currentScope != nil {
				if sym, ok := a.currentScope.Lookup(id.Name); ok && sym != nil {
					return indexTypeGuaranteedNonNegative(sym.Type)
				}
			}
		}
	}
	return false
}

func syntacticCrossMeasureDecreases(caller, callee *ast.FuncDecl, call *ast.CallExpr, k int) bool {
	if caller == nil || callee == nil || call == nil {
		return false
	}
	callerMeasures := decreaseMeasureComponents(caller.Decreases)
	calleeMeasures := decreaseMeasureComponents(callee.Decreases)
	if k >= len(callerMeasures) || k >= len(calleeMeasures) {
		return false
	}
	callerID, ok := callerMeasures[k].(*ast.Ident)
	if !ok || callerID == nil {
		return false
	}
	calleeID, ok := calleeMeasures[k].(*ast.Ident)
	if !ok || calleeID == nil {
		return false
	}
	calleeParam := -1
	for i, param := range callee.Params {
		if param.Name == calleeID.Name {
			calleeParam = i
			break
		}
	}
	args := proofCallArgs(call)
	if calleeParam < 0 || calleeParam >= len(args) {
		return false
	}
	bin, ok := stripOptimizationParens(args[calleeParam]).(*ast.BinaryExpr)
	if !ok || bin == nil || bin.Op != lexer.TOKEN_MINUS {
		return false
	}
	left, ok := stripOptimizationParens(bin.Left).(*ast.Ident)
	if !ok || left == nil || left.Name != callerID.Name {
		return false
	}
	lit, ok := stripOptimizationParens(bin.Right).(*ast.IntLit)
	if !ok || lit == nil {
		return false
	}
	v, ok := parsePositiveIntLiteral(lit)
	if ok && v > 0 {
		return true
	}
	return false
}

func parsePositiveIntLiteral(lit *ast.IntLit) (int64, bool) {
	if lit == nil || lit.IsHex || lit.Suffix != "" {
		return 0, false
	}
	var v int64
	for _, ch := range lit.Value {
		if ch == '_' {
			continue
		}
		if ch < '0' || ch > '9' {
			return 0, false
		}
		v = v*10 + int64(ch-'0')
		if v <= 0 {
			return 0, false
		}
	}
	return v, true
}

func (a *Analyzer) crossMeasureDiff(callerMeasure, calleeMeasure ast.Expr, subst map[string]ast.Expr) (affineForm, bool) {
	entry, ok := a.affineOf(callerMeasure, a.currentScope)
	if !ok {
		return affineForm{}, false
	}
	call, ok := a.substitutedAffine(calleeMeasure, subst)
	if !ok {
		return affineForm{}, false
	}
	return subtractAffine(entry, call), true
}

func (a *Analyzer) crossMeasureStrictlyDecreases(callerMeasure, calleeMeasure ast.Expr, subst map[string]ast.Expr) bool {
	diff, ok := a.crossMeasureDiff(callerMeasure, calleeMeasure, subst)
	if !ok {
		return false
	}
	r := a.boundAffine(diff, a.currentScope)
	return r.loKnown && r.lo > 0
}

func (a *Analyzer) crossMeasureDiffIsZero(callerMeasure, calleeMeasure ast.Expr, subst map[string]ast.Expr) bool {
	diff, ok := a.crossMeasureDiff(callerMeasure, calleeMeasure, subst)
	if !ok {
		return false
	}
	r := a.boundAffine(diff, a.currentScope)
	return r.loKnown && r.hiKnown && r.lo == 0 && r.hi == 0
}
