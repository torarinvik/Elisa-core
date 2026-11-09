package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) validateStaticFunctionRecursion(fn *ast.FuncDecl) {
	a.walkStaticStmts(fn.Body, func(expr ast.Expr) bool {
		call, ok := expr.(*ast.CallExpr)
		if ok {
			a.validateStaticRecursiveCall(fn, call)
		}
		return false
	})
}

func (a *Analyzer) validateStaticRecursiveCall(fn *ast.FuncDecl, call *ast.CallExpr) {
	if fn == nil || call == nil {
		return
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok {
		return
	}
	if ident.Name != fn.Name {
		a.validateStaticIndirectRecursiveCall(fn, call, ident.Name)
		return
	}
	if paramName, ok := a.staticRecursiveCallDecreasingParam(fn, call); ok {
		if a.staticFunctionHasBaseCaseForParam(fn, paramName) {
			return
		}
		if !a.staticFunctionParamIsUnsigned(fn, paramName) && a.staticFunctionHasWeakSignedZeroBaseCaseForParam(fn, paramName) {
			a.errorf(call.Pos(), "recursive static call to %q uses `== 0` as the only visible base case for signed parameter %q; use a lower-bound check such as `%s <= 0`", fn.Name, paramName, paramName)
			return
		}
		a.errorf(call.Pos(), "recursive static call to %q must have a visible terminating base case for parameter %q", fn.Name, paramName)
		return
	}
	a.errorf(call.Pos(), "recursive static call to %q must decrease a parameter using parameter - positive_constant", fn.Name)
}

func (a *Analyzer) validateStaticIndirectRecursiveCall(fn *ast.FuncDecl, call *ast.CallExpr, calleeName string) {
	callee := a.staticFunctionDeclByName(calleeName)
	if callee == nil || callee == fn {
		return
	}
	if !a.staticFunctionEventuallyCalls(callee, fn.Name, map[*ast.FuncDecl]bool{}) {
		return
	}
	a.errorf(call.Pos(), "indirect recursive static call cycle involving %q and %q is not supported; use direct structurally decreasing recursion", fn.Name, callee.Name)
}

func (a *Analyzer) staticFunctionDeclByName(name string) *ast.FuncDecl {
	sym, _, ok := a.lookupVisibleGlobal(name)
	if !ok || sym == nil {
		return nil
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || !fnType.Static {
		return nil
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || decl == nil || !decl.Static {
		return nil
	}
	return decl
}

func (a *Analyzer) staticFunctionEventuallyCalls(fn *ast.FuncDecl, targetName string, seen map[*ast.FuncDecl]bool) bool {
	if fn == nil {
		return false
	}
	if seen[fn] {
		return false
	}
	seen[fn] = true
	return a.walkStaticStmts(fn.Body, func(expr ast.Expr) bool {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		ident, ok := call.Func.(*ast.Ident)
		if !ok {
			return false
		}
		if ident.Name == targetName {
			return true
		}
		callee := a.staticFunctionDeclByName(ident.Name)
		return callee != nil && a.staticFunctionEventuallyCalls(callee, targetName, seen)
	})
}

func (a *Analyzer) walkStaticStmts(stmts []ast.Stmt, visitExpr func(ast.Expr) bool) bool {
	for _, stmt := range stmts {
		if a.walkStaticStmt(stmt, visitExpr) {
			return true
		}
	}
	return false
}

func (a *Analyzer) walkStaticStmt(stmt ast.Stmt, visitExpr func(ast.Expr) bool) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt:
		return a.walkStaticExpr(n.Value, visitExpr)
	case *ast.LetDestructureStmt:
		return a.walkStaticExpr(n.Value, visitExpr)
	case *ast.TupleBindStmt:
		return a.walkStaticExpr(n.Value, visitExpr)
	case *ast.MoveBindStmt:
		return a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticExpr(n.Store, visitExpr)
	case *ast.VarDeclStmt:
		return a.walkStaticExpr(n.Value, visitExpr)
	case *ast.AssignStmt:
		return a.walkStaticExpr(n.Target, visitExpr) || a.walkStaticExpr(n.Value, visitExpr)
	case *ast.AugAssignStmt:
		if n.CollectionAppend != nil {
			return a.walkStaticExpr(n.CollectionAppend, visitExpr)
		}
		return a.walkStaticExpr(n.Target, visitExpr) || a.walkStaticExpr(n.Value, visitExpr)
	case *ast.AsRefAssignStmt:
		return a.walkStaticExpr(n.Target, visitExpr) || a.walkStaticExpr(n.Value, visitExpr)
	case *ast.ExprStmt:
		return a.walkStaticExpr(n.Expr, visitExpr)
	case *ast.StaticAssertStmt:
		return a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticExpr(n.Message, visitExpr)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			if a.walkStaticExpr(item.Cond, visitExpr) || a.walkStaticExpr(item.Message, visitExpr) {
				return true
			}
		}
		return false
	case *ast.StaticErrorStmt:
		return a.walkStaticExpr(n.Message, visitExpr)
	case *ast.IfStmt:
		if a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticStmts(n.Then, visitExpr) || a.walkStaticStmts(n.Else, visitExpr) {
			return true
		}
		for _, elif := range n.Elifs {
			if a.walkStaticExpr(elif.Cond, visitExpr) || a.walkStaticStmts(elif.Body, visitExpr) {
				return true
			}
		}
	case *ast.StaticIfStmt:
		if a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticStmts(n.Then, visitExpr) || a.walkStaticStmts(n.Else, visitExpr) {
			return true
		}
		for _, elif := range n.Elifs {
			if a.walkStaticExpr(elif.Cond, visitExpr) || a.walkStaticStmts(elif.Body, visitExpr) {
				return true
			}
		}
	case *ast.StaticBlockStmt:
		return a.walkStaticStmts(n.Body, visitExpr)
	case *ast.DeferStmt:
		return a.walkStaticStmts(n.Body, visitExpr)
	case *ast.WhileStmt:
		return a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.ForStmt:
		return a.walkStaticExpr(n.Start, visitExpr) || a.walkStaticExpr(n.End, visitExpr) || a.walkStaticExpr(n.Step, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.IterForStmt:
		return a.walkStaticExpr(n.Source, visitExpr) || a.walkStaticExpr(n.WhereFilter, visitExpr) || a.walkStaticExpr(n.Filter, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.ParallelForStmt:
		return a.walkStaticExpr(n.Source, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.MatchStmt:
		if a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticExpr(n.Store, visitExpr) {
			return true
		}
		for _, arm := range n.Arms {
			if a.walkStaticExpr(arm.Guard, visitExpr) || a.walkStaticStmts(arm.Body, visitExpr) {
				return true
			}
		}
	case *ast.ExpectPatternStmt:
		return a.walkStaticExpr(n.Value, visitExpr)
	case *ast.InStoreStmt:
		return a.walkStaticExpr(n.Store, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.CanStmt:
		for _, arg := range n.HandlerArgs {
			if a.walkStaticExpr(arg, visitExpr) {
				return true
			}
		}
		return a.walkStaticStmts(n.Body, visitExpr)
	case *ast.ScopeStmt:
		return a.walkStaticExpr(n.Guard, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.PoolStmt:
		return a.walkStaticExpr(n.Workers, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.LockStmt:
		return a.walkStaticExpr(n.Mutex, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.PanicStmt:
		return a.walkStaticExpr(n.Message, visitExpr)
	case *ast.AssertByStmt:
		return a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticStmts(n.Proof, visitExpr)
	case *ast.ProofBlockStmt:
		return a.walkStaticExpr(n.Goal, visitExpr) || a.walkStaticStmts(n.Proof, visitExpr)
	case *ast.ProofUseStmt:
		for _, citation := range n.Citations {
			if a.walkStaticExpr(citation, visitExpr) {
				return true
			}
		}
	case *ast.ContractStmt:
		if a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticStmts(n.Proof, visitExpr) {
			return true
		}
		for _, arg := range n.UsesArgs {
			if a.walkStaticExpr(arg, visitExpr) {
				return true
			}
		}
	case *ast.DiscardStmt:
		return a.walkStaticExpr(n.Value, visitExpr)
	case *ast.RegionStmt:
		return a.walkStaticExpr(n.Capacity, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.PromoteStmt:
		return a.walkStaticExpr(n.Value, visitExpr)
	case *ast.CheckpointStmt:
		return a.walkStaticExpr(n.Target, visitExpr) || a.walkStaticStmts(n.Body, visitExpr)
	case *ast.GroupedCheckpointStmt:
		for _, target := range n.Targets {
			if a.walkStaticExpr(target, visitExpr) {
				return true
			}
		}
		return a.walkStaticStmts(n.Body, visitExpr)
	case *ast.MachineCoverageStmt:
		return a.walkStaticExpr(n.Input, visitExpr)
	}
	return false
}

func (a *Analyzer) walkStaticExpr(expr ast.Expr, visitExpr func(ast.Expr) bool) bool {
	if expr == nil {
		return false
	}
	if visitExpr != nil && visitExpr(expr) {
		return true
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		return a.walkStaticExpr(n.Left, visitExpr) || a.walkStaticExpr(n.Right, visitExpr)
	case *ast.UnaryExpr:
		return a.walkStaticExpr(n.Operand, visitExpr)
	case *ast.MoveExpr:
		return a.walkStaticExpr(n.Operand, visitExpr)
	case *ast.ParenExpr:
		return a.walkStaticExpr(n.Inner, visitExpr)
	case *ast.CastExpr:
		return a.walkStaticExpr(n.Operand, visitExpr)
	case *ast.TernaryExpr:
		return a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticExpr(n.Cond, visitExpr) || a.walkStaticExpr(n.Alt, visitExpr)
	case *ast.FieldExpr:
		return a.walkStaticExpr(n.Object, visitExpr)
	case *ast.IndexExpr:
		return a.walkStaticExpr(n.Object, visitExpr) || a.walkStaticExpr(n.Index, visitExpr) || a.walkStaticExpr(n.Index2, visitExpr) || a.walkStaticExpr(n.Fallback, visitExpr)
	case *ast.SliceExpr:
		return a.walkStaticExpr(n.Object, visitExpr) || a.walkStaticExpr(n.Start, visitExpr) || a.walkStaticExpr(n.End, visitExpr)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			if a.walkStaticExpr(elem, visitExpr) {
				return true
			}
		}
		for _, key := range n.Keys {
			if a.walkStaticExpr(key, visitExpr) {
				return true
			}
		}
		return a.walkStaticExpr(n.Owner, visitExpr)
	case *ast.MembershipRangeExpr:
		return a.walkStaticExpr(n.Start, visitExpr) || a.walkStaticExpr(n.End, visitExpr)
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			if a.walkStaticExpr(elem, visitExpr) {
				return true
			}
		}
	case *ast.CallExpr:
		if a.walkStaticExpr(n.Func, visitExpr) || a.walkStaticExpr(n.SafeReceiver, visitExpr) {
			return true
		}
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		for _, arg := range args {
			if a.walkStaticExpr(arg, visitExpr) {
				return true
			}
		}
	case *ast.StructLitExpr:
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		for _, arg := range args {
			if a.walkStaticExpr(arg, visitExpr) {
				return true
			}
		}
		for _, spread := range n.Spreads {
			if a.walkStaticExpr(spread, visitExpr) {
				return true
			}
		}
	case *ast.RecordUpdateExpr:
		if a.walkStaticExpr(n.Base, visitExpr) {
			return true
		}
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		for _, arg := range args {
			if a.walkStaticExpr(arg, visitExpr) {
				return true
			}
		}
	case *ast.ListComprehensionExpr:
		return a.walkStaticStmts(n.Bindings, visitExpr) || a.walkStaticExpr(n.Key, visitExpr) || a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticExpr(n.Source, visitExpr) || a.walkStaticExpr(n.RangeEnd, visitExpr) || a.walkStaticExpr(n.RangeStep, visitExpr) || a.walkStaticExpr(n.Filter, visitExpr) || a.walkStaticExpr(n.Owner, visitExpr) || a.walkStaticExpr(n.LoweredParallel, visitExpr)
	case *ast.QueryExpr:
		return a.walkStaticExpr(n.Source, visitExpr) || a.walkStaticExpr(n.Filter, visitExpr) || a.walkStaticExpr(n.Projection, visitExpr) || a.walkStaticExpr(n.Owner, visitExpr)
	case *ast.LambdaExpr:
		return a.walkStaticStmts(n.Body, visitExpr) || a.walkStaticExpr(n.BodyExpr, visitExpr)
	case *ast.AddrOfExpr:
		return a.walkStaticExpr(n.Operand, visitExpr)
	case *ast.SpecializeExpr:
		return a.walkStaticExpr(n.Operand, visitExpr)
	case *ast.OptionalBindExpr:
		return a.walkStaticExpr(n.Value, visitExpr)
	case *ast.AllocExpr:
		return a.walkStaticExpr(n.Owner, visitExpr) || a.walkStaticExpr(n.Value, visitExpr)
	case *ast.CanExpr:
		return a.walkStaticExpr(n.Expr, visitExpr)
	case *ast.RaiseExpr:
		return a.walkStaticExpr(n.Error, visitExpr)
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			if a.walkStaticExpr(target, visitExpr) {
				return true
			}
		}
	case *ast.IsAliasExpr:
		return a.walkStaticExpr(n.Target, visitExpr)
	case *ast.ExprBlock:
		return a.walkStaticStmts(n.Stmts, visitExpr) || a.walkStaticExpr(n.Value, visitExpr)
	case *ast.FoldExpr:
		if a.walkStaticExpr(n.Value, visitExpr) {
			return true
		}
		for _, arm := range n.Arms {
			if a.walkStaticExpr(arm.Guard, visitExpr) || a.walkStaticStmts(arm.Body, visitExpr) {
				return true
			}
		}
	case *ast.EmitExpr:
		return a.walkStaticExpr(n.Value, visitExpr)
	// Optional/error-unwrap and try/catch/match EXPRESSIONS wrap a value expression
	// that can itself contain calls (e.g. `get parser.unary_expr()`). Descending into
	// them is required for soundness: a recursive call hidden inside `get`/`else`/`try`
	// would otherwise be invisible to the termination call collector, so its `decreases`
	// obligation would be silently skipped (a function could be "proven" terminating
	// while a hidden call diverges).
	case *ast.GetExpr:
		if a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticExpr(n.Fallback, visitExpr) {
			return true
		}
		if n.Recovery != nil {
			return a.walkStaticExpr(n.Recovery.Value, visitExpr) || a.walkStaticStmts(n.Recovery.Body, visitExpr)
		}
	case *ast.UnwrapElseExpr:
		if a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticExpr(n.Fallback, visitExpr) {
			return true
		}
		if n.Recovery != nil {
			return a.walkStaticExpr(n.Recovery.Value, visitExpr) || a.walkStaticStmts(n.Recovery.Body, visitExpr)
		}
	case *ast.TryExpr:
		if a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticExpr(n.Fallback, visitExpr) {
			return true
		}
		if n.Recovery != nil {
			return a.walkStaticExpr(n.Recovery.Value, visitExpr) || a.walkStaticStmts(n.Recovery.Body, visitExpr)
		}
	case *ast.CatchExpr:
		if a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticStmts(n.Success.Body, visitExpr) {
			return true
		}
		for _, arm := range n.Arms {
			if a.walkStaticStmts(arm.Body, visitExpr) {
				return true
			}
		}
	case *ast.MatchExpr:
		if a.walkStaticExpr(n.Value, visitExpr) || a.walkStaticExpr(n.Store, visitExpr) {
			return true
		}
		for _, arm := range n.Arms {
			if a.walkStaticExpr(arm.Guard, visitExpr) || a.walkStaticStmts(arm.Body, visitExpr) {
				return true
			}
		}
	case *ast.QuantifierExpr:
		return a.walkStaticExpr(n.In, visitExpr) || a.walkStaticExpr(n.Body, visitExpr)
	case *ast.MachineFromExpr:
		for _, arg := range n.StartArgs {
			if a.walkStaticExpr(arg, visitExpr) {
				return true
			}
		}
		if a.walkStaticExpr(n.Decreases, visitExpr) {
			return true
		}
		for _, arm := range n.Arms {
			if a.walkStaticStmts(arm.Body, visitExpr) {
				return true
			}
			for _, term := range arm.Terminators {
				if a.walkStaticExpr(term.Guard, visitExpr) || a.walkStaticExpr(term.Value, visitExpr) {
					return true
				}
				for _, arg := range term.Args {
					if a.walkStaticExpr(arg, visitExpr) {
						return true
					}
				}
			}
		}
	}
	return false
}
