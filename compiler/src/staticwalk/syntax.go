package staticwalk

import "elisacore/src/ast"

func Statements(stmts []ast.Stmt, visitExpr func(ast.Expr) bool) bool {
	for _, stmt := range stmts {
		if Statement(stmt, visitExpr) {
			return true
		}
	}
	return false
}

func Statement(stmt ast.Stmt, visitExpr func(ast.Expr) bool) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt:
		return Expression(n.Value, visitExpr)
	case *ast.LetDestructureStmt:
		return Expression(n.Value, visitExpr)
	case *ast.TupleBindStmt:
		return Expression(n.Value, visitExpr)
	case *ast.MoveBindStmt:
		return Expression(n.Value, visitExpr) || Expression(n.Store, visitExpr)
	case *ast.VarDeclStmt:
		return Expression(n.Value, visitExpr)
	case *ast.AssignStmt:
		return Expression(n.Target, visitExpr) || Expression(n.Value, visitExpr)
	case *ast.AugAssignStmt:
		if n.CollectionAppend != nil {
			return Expression(n.CollectionAppend, visitExpr)
		}
		return Expression(n.Target, visitExpr) || Expression(n.Value, visitExpr)
	case *ast.AsRefAssignStmt:
		return Expression(n.Target, visitExpr) || Expression(n.Value, visitExpr)
	case *ast.ExprStmt:
		return Expression(n.Expr, visitExpr)
	case *ast.StaticAssertStmt:
		return Expression(n.Cond, visitExpr) || Expression(n.Message, visitExpr)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			if Expression(item.Cond, visitExpr) || Expression(item.Message, visitExpr) {
				return true
			}
		}
		return false
	case *ast.StaticErrorStmt:
		return Expression(n.Message, visitExpr)
	case *ast.IfStmt:
		if Expression(n.Cond, visitExpr) || Statements(n.Then, visitExpr) || Statements(n.Else, visitExpr) {
			return true
		}
		for _, elif := range n.Elifs {
			if Expression(elif.Cond, visitExpr) || Statements(elif.Body, visitExpr) {
				return true
			}
		}
	case *ast.StaticIfStmt:
		if Expression(n.Cond, visitExpr) || Statements(n.Then, visitExpr) || Statements(n.Else, visitExpr) {
			return true
		}
		for _, elif := range n.Elifs {
			if Expression(elif.Cond, visitExpr) || Statements(elif.Body, visitExpr) {
				return true
			}
		}
	case *ast.StaticBlockStmt:
		return Statements(n.Body, visitExpr)
	case *ast.DeferStmt:
		return Statements(n.Body, visitExpr)
	case *ast.WhileStmt:
		return Expression(n.Cond, visitExpr) || Statements(n.Body, visitExpr)
	case *ast.ForStmt:
		return Expression(n.Start, visitExpr) || Expression(n.End, visitExpr) || Expression(n.Step, visitExpr) || Statements(n.Body, visitExpr)
	case *ast.IterForStmt:
		return Expression(n.Source, visitExpr) || Expression(n.WhereFilter, visitExpr) || Expression(n.Filter, visitExpr) || Statements(n.Body, visitExpr)
	case *ast.ParallelForStmt:
		return Expression(n.Source, visitExpr) || Statements(n.Body, visitExpr)
	case *ast.MatchStmt:
		if Expression(n.Value, visitExpr) || Expression(n.Store, visitExpr) {
			return true
		}
		for _, arm := range n.Arms {
			if Expression(arm.Guard, visitExpr) || Statements(arm.Body, visitExpr) {
				return true
			}
		}
	case *ast.ExpectPatternStmt:
		return Expression(n.Value, visitExpr)
	case *ast.InStoreStmt:
		return Expression(n.Store, visitExpr) || Statements(n.Body, visitExpr)
	case *ast.CanStmt:
		for _, arg := range n.HandlerArgs {
			if Expression(arg, visitExpr) {
				return true
			}
		}
		return Statements(n.Body, visitExpr)
	case *ast.ScopeStmt:
		return Expression(n.Guard, visitExpr) || Statements(n.Body, visitExpr)
	case *ast.PoolStmt:
		return Expression(n.Workers, visitExpr) || Statements(n.Body, visitExpr)
	case *ast.LockStmt:
		return Expression(n.Mutex, visitExpr) || Statements(n.Body, visitExpr)
	case *ast.PanicStmt:
		return Expression(n.Message, visitExpr)
	case *ast.AssertByStmt:
		return Expression(n.Cond, visitExpr) || Statements(n.Proof, visitExpr)
	case *ast.ProofBlockStmt:
		return Expression(n.Goal, visitExpr) || Statements(n.Proof, visitExpr)
	case *ast.ProofUseStmt:
		for _, citation := range n.Citations {
			if Expression(citation, visitExpr) {
				return true
			}
		}
	case *ast.ContractStmt:
		if Expression(n.Cond, visitExpr) || Statements(n.Proof, visitExpr) {
			return true
		}
		for _, arg := range n.UsesArgs {
			if Expression(arg, visitExpr) {
				return true
			}
		}
	case *ast.DiscardStmt:
		return Expression(n.Value, visitExpr)
	case *ast.RegionStmt:
		return Expression(n.Capacity, visitExpr) || Statements(n.Body, visitExpr)
	case *ast.PromoteStmt:
		return Expression(n.Value, visitExpr)
	case *ast.CheckpointStmt:
		return Expression(n.Target, visitExpr) || Statements(n.Body, visitExpr)
	case *ast.GroupedCheckpointStmt:
		for _, target := range n.Targets {
			if Expression(target, visitExpr) {
				return true
			}
		}
		return Statements(n.Body, visitExpr)
	case *ast.MachineCoverageStmt:
		return Expression(n.Input, visitExpr)
	}
	return false
}

func Expression(expr ast.Expr, visitExpr func(ast.Expr) bool) bool {
	if expr == nil {
		return false
	}
	if visitExpr != nil && visitExpr(expr) {
		return true
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		return Expression(n.Left, visitExpr) || Expression(n.Right, visitExpr)
	case *ast.UnaryExpr:
		return Expression(n.Operand, visitExpr)
	case *ast.MoveExpr:
		return Expression(n.Operand, visitExpr)
	case *ast.ParenExpr:
		return Expression(n.Inner, visitExpr)
	case *ast.CastExpr:
		return Expression(n.Operand, visitExpr)
	case *ast.TernaryExpr:
		return Expression(n.Value, visitExpr) || Expression(n.Cond, visitExpr) || Expression(n.Alt, visitExpr)
	case *ast.FieldExpr:
		return Expression(n.Object, visitExpr)
	case *ast.IndexExpr:
		return Expression(n.Object, visitExpr) || Expression(n.Index, visitExpr) || Expression(n.Index2, visitExpr) || Expression(n.Fallback, visitExpr)
	case *ast.SliceExpr:
		return Expression(n.Object, visitExpr) || Expression(n.Start, visitExpr) || Expression(n.End, visitExpr)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			if Expression(elem, visitExpr) {
				return true
			}
		}
		for _, key := range n.Keys {
			if Expression(key, visitExpr) {
				return true
			}
		}
		return Expression(n.Owner, visitExpr)
	case *ast.MembershipRangeExpr:
		return Expression(n.Start, visitExpr) || Expression(n.End, visitExpr)
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			if Expression(elem, visitExpr) {
				return true
			}
		}
	case *ast.CallExpr:
		if Expression(n.Func, visitExpr) || Expression(n.SafeReceiver, visitExpr) {
			return true
		}
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		for _, arg := range args {
			if Expression(arg, visitExpr) {
				return true
			}
		}
	case *ast.StructLitExpr:
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		for _, arg := range args {
			if Expression(arg, visitExpr) {
				return true
			}
		}
		for _, spread := range n.Spreads {
			if Expression(spread, visitExpr) {
				return true
			}
		}
	case *ast.RecordUpdateExpr:
		if Expression(n.Base, visitExpr) {
			return true
		}
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		for _, arg := range args {
			if Expression(arg, visitExpr) {
				return true
			}
		}
	case *ast.ListComprehensionExpr:
		return Statements(n.Bindings, visitExpr) || Expression(n.Key, visitExpr) || Expression(n.Value, visitExpr) || Expression(n.Source, visitExpr) || Expression(n.RangeEnd, visitExpr) || Expression(n.RangeStep, visitExpr) || Expression(n.Filter, visitExpr) || Expression(n.Owner, visitExpr) || Expression(n.LoweredParallel, visitExpr)
	case *ast.QueryExpr:
		return Expression(n.Source, visitExpr) || Expression(n.Filter, visitExpr) || Expression(n.Projection, visitExpr) || Expression(n.Owner, visitExpr)
	case *ast.LambdaExpr:
		return Statements(n.Body, visitExpr) || Expression(n.BodyExpr, visitExpr)
	case *ast.AddrOfExpr:
		return Expression(n.Operand, visitExpr)
	case *ast.SpecializeExpr:
		return Expression(n.Operand, visitExpr)
	case *ast.OptionalBindExpr:
		return Expression(n.Value, visitExpr)
	case *ast.AllocExpr:
		return Expression(n.Owner, visitExpr) || Expression(n.Value, visitExpr)
	case *ast.CanExpr:
		return Expression(n.Expr, visitExpr)
	case *ast.RaiseExpr:
		return Expression(n.Error, visitExpr)
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			if Expression(target, visitExpr) {
				return true
			}
		}
	case *ast.IsAliasExpr:
		return Expression(n.Target, visitExpr)
	case *ast.ExprBlock:
		return Statements(n.Stmts, visitExpr) || Expression(n.Value, visitExpr)
	case *ast.FoldExpr:
		if Expression(n.Value, visitExpr) {
			return true
		}
		for _, arm := range n.Arms {
			if Expression(arm.Guard, visitExpr) || Statements(arm.Body, visitExpr) {
				return true
			}
		}
	case *ast.EmitExpr:
		return Expression(n.Value, visitExpr)
	// Optional/error-unwrap and try/catch/match EXPRESSIONS wrap a value expression
	// that can itself contain calls (e.g. `get parser.unary_expr()`). Descending into
	// them is required for soundness: a recursive call hidden inside `get`/`else`/`try`
	// would otherwise be invisible to the termination call collector, so its `decreases`
	// obligation would be silently skipped (a function could be "proven" terminating
	// while a hidden call diverges).
	case *ast.GetExpr:
		if Expression(n.Value, visitExpr) || Expression(n.Fallback, visitExpr) {
			return true
		}
		if n.Recovery != nil {
			return Expression(n.Recovery.Value, visitExpr) || Statements(n.Recovery.Body, visitExpr)
		}
	case *ast.UnwrapElseExpr:
		if Expression(n.Value, visitExpr) || Expression(n.Fallback, visitExpr) {
			return true
		}
		if n.Recovery != nil {
			return Expression(n.Recovery.Value, visitExpr) || Statements(n.Recovery.Body, visitExpr)
		}
	case *ast.TryExpr:
		if Expression(n.Value, visitExpr) || Expression(n.Fallback, visitExpr) {
			return true
		}
		if n.Recovery != nil {
			return Expression(n.Recovery.Value, visitExpr) || Statements(n.Recovery.Body, visitExpr)
		}
	case *ast.CatchExpr:
		if Expression(n.Value, visitExpr) || Statements(n.Success.Body, visitExpr) {
			return true
		}
		for _, arm := range n.Arms {
			if Statements(arm.Body, visitExpr) {
				return true
			}
		}
	case *ast.MatchExpr:
		if Expression(n.Value, visitExpr) || Expression(n.Store, visitExpr) {
			return true
		}
		for _, arm := range n.Arms {
			if Expression(arm.Guard, visitExpr) || Statements(arm.Body, visitExpr) {
				return true
			}
		}
	case *ast.QuantifierExpr:
		return Expression(n.In, visitExpr) || Expression(n.Body, visitExpr)
	case *ast.MachineFromExpr:
		for _, arg := range n.StartArgs {
			if Expression(arg, visitExpr) {
				return true
			}
		}
		if Expression(n.Decreases, visitExpr) {
			return true
		}
		for _, arm := range n.Arms {
			if Statements(arm.Body, visitExpr) {
				return true
			}
			for _, term := range arm.Terminators {
				if Expression(term.Guard, visitExpr) || Expression(term.Value, visitExpr) {
					return true
				}
				for _, arg := range term.Args {
					if Expression(arg, visitExpr) {
						return true
					}
				}
			}
		}
	}
	return false
}
