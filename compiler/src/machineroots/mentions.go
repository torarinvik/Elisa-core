package machineroots

import "elisacore/src/ast"

func ExprMentionsIdent(expr ast.Expr, name string) bool {
	found := false
	var walk func(ast.Expr)
	var walkStatements func([]ast.Stmt)
	walk = func(e ast.Expr) {
		if found || e == nil {
			return
		}
		switch n := e.(type) {
		case *ast.Ident:
			if n.Name == name {
				found = true
			}
		case *ast.FieldExpr:
			walk(n.Object)
		case *ast.CallExpr:
			walk(n.Func)
			for _, a := range n.Args {
				walk(a)
			}
		case *ast.IndexExpr:
			walk(n.Object)
			walk(n.Index)
			walk(n.Index2)
			walk(n.Fallback)
		case *ast.SliceExpr:
			walk(n.Object)
			walk(n.Start)
			walk(n.End)
		case *ast.CastExpr:
			walk(n.Operand)
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.UnaryExpr:
			walk(n.Operand)
		case *ast.ParenExpr:
			walk(n.Inner)
		case *ast.ListLitExpr:
			for _, elem := range n.Elems {
				walk(elem)
			}
			for _, key := range n.Keys {
				walk(key)
			}
			walk(n.Owner)
		case *ast.MembershipRangeExpr:
			walk(n.Start)
			walk(n.End)
		case *ast.ListComprehensionExpr:
			walk(n.Value)
			walk(n.Source)
			walk(n.RangeEnd)
			walk(n.RangeStep)
			walk(n.Filter)
			walk(n.Owner)
			walk(n.Key)
			walk(n.LoweredParallel)
			walkStatements(n.Bindings)
		case *ast.QueryExpr:
			walk(n.Source)
			walk(n.Filter)
			walk(n.Projection)
			walk(n.Owner)
		case *ast.TernaryExpr:
			walk(n.Value)
			walk(n.Cond)
			walk(n.Alt)
		case *ast.AddrOfExpr:
			walk(n.Operand)
		case *ast.SpecializeExpr:
			walk(n.Operand)
		case *ast.StructLitExpr:
			for _, arg := range n.Args {
				walk(arg)
			}
			for _, spread := range n.Spreads {
				walk(spread)
			}
		case *ast.RecordUpdateExpr:
			walk(n.Base)
			for _, arg := range n.Args {
				walk(arg)
			}
		case *ast.TupleExpr:
			for _, elem := range n.Elems {
				walk(elem)
			}
		case *ast.OptionalBindExpr:
			walk(n.Value)
		case *ast.AllocExpr:
			walk(n.Owner)
			walk(n.Value)
		case *ast.CanExpr:
			walk(n.Expr)
		case *ast.RaiseExpr:
			walk(n.Error)
		case *ast.IsPatternExpr:
			for _, target := range n.Targets {
				walk(target)
			}
		case *ast.IsAliasExpr:
			walk(n.Target)
		case *ast.ExprBlock:
			walkStatements(n.Stmts)
			walk(n.Value)
		case *ast.LambdaExpr:
			walkStatements(n.Body)
			walk(n.BodyExpr)
		case *ast.FoldExpr:
			walk(n.Value)
			for _, arm := range n.Arms {
				walk(arm.Guard)
				walkStatements(arm.Body)
			}
		case *ast.EmitExpr:
			walk(n.Value)
		case *ast.TryExpr:
			walk(n.Value)
			walk(n.Fallback)
			if n.Recovery != nil {
				walk(n.Recovery.Value)
				walkStatements(n.Recovery.Body)
			}
		case *ast.GetExpr:
			walk(n.Value)
			walk(n.Fallback)
			if n.Recovery != nil {
				walk(n.Recovery.Value)
				walkStatements(n.Recovery.Body)
			}
		case *ast.UnwrapElseExpr:
			walk(n.Value)
			walk(n.Fallback)
			if n.Recovery != nil {
				walk(n.Recovery.Value)
				walkStatements(n.Recovery.Body)
			}
		case *ast.CatchExpr:
			walk(n.Value)
			walkStatements(n.Success.Body)
			for _, arm := range n.Arms {
				walkStatements(arm.Body)
			}
		case *ast.MatchExpr:
			walk(n.Value)
			walk(n.Store)
			for _, arm := range n.Arms {
				walk(arm.Guard)
				walkStatements(arm.Body)
			}
		}
	}
	walkStatements = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			if found {
				return
			}
			switch s := stmt.(type) {
			case *ast.ExprStmt:
				walk(s.Expr)
			case *ast.AssignStmt:
				walk(s.Target)
				walk(s.Value)
			case *ast.AugAssignStmt:
				walk(s.Target)
				walk(s.Value)
			case *ast.AsRefAssignStmt:
				walk(s.Target)
				walk(s.Value)
			case *ast.VarDeclStmt:
				walk(s.Value)
			case *ast.LetDestructureStmt:
				walk(s.Value)
			case *ast.TupleBindStmt:
				walk(s.Value)
			case *ast.MoveBindStmt:
				walk(s.Value)
				walk(s.Store)
			case *ast.DeferStmt:
				walkStatements(s.Body)
			case *ast.ReturnStmt:
				walk(s.Value)
			case *ast.IfStmt:
				walk(s.Cond)
				walkStatements(s.Then)
				for _, clause := range s.Elifs {
					walk(clause.Cond)
					walkStatements(clause.Body)
				}
				walkStatements(s.Else)
			case *ast.WhileStmt:
				walk(s.Cond)
				walkStatements(s.Body)
			case *ast.ForStmt:
				walk(s.Start)
				walk(s.End)
				walk(s.Step)
				walkStatements(s.Body)
			case *ast.IterForStmt:
				walk(s.Source)
				walk(s.WhereFilter)
				walk(s.Filter)
				walkStatements(s.Body)
			case *ast.ParallelForStmt:
				walk(s.Source)
				walkStatements(s.Body)
			case *ast.MatchStmt:
				walk(s.Value)
				walk(s.Store)
				for _, arm := range s.Arms {
					walk(arm.Guard)
					walkStatements(arm.Body)
				}
			case *ast.ExpectPatternStmt:
				walk(s.Value)
			case *ast.InStoreStmt:
				walk(s.Store)
				walkStatements(s.Body)
			case *ast.CanStmt:
				for _, arg := range s.HandlerArgs {
					walk(arg)
				}
				walkStatements(s.Body)
			case *ast.ScopeStmt:
				walk(s.Guard)
				walkStatements(s.Body)
			case *ast.PoolStmt:
				walk(s.Workers)
				walkStatements(s.Body)
			case *ast.LockStmt:
				walk(s.Mutex)
				walkStatements(s.Body)
			case *ast.PanicStmt:
				walk(s.Message)
			case *ast.StaticIfStmt:
				walk(s.Cond)
				walkStatements(s.Then)
				for _, clause := range s.Elifs {
					walk(clause.Cond)
					walkStatements(clause.Body)
				}
				walkStatements(s.Else)
			case *ast.StaticErrorStmt:
				walk(s.Message)
			case *ast.StaticAssertStmt:
				walk(s.Cond)
				walk(s.Message)
			case *ast.AssertByStmt:
				walk(s.Cond)
				walkStatements(s.Proof)
			case *ast.ProofBlockStmt:
				walk(s.Goal)
				walkStatements(s.Proof)
			case *ast.ProofUseStmt:
				for _, citation := range s.Citations {
					walk(citation)
				}
			case *ast.ContractStmt:
				walk(s.Cond)
				walkStatements(s.Proof)
				for _, arg := range s.UsesArgs {
					walk(arg)
				}
			case *ast.StaticBlockStmt:
				walkStatements(s.Body)
			case *ast.DiscardStmt:
				walk(s.Value)
			case *ast.RegionStmt:
				walk(s.Capacity)
				walkStatements(s.Body)
			case *ast.PromoteStmt:
				walk(s.Value)
			case *ast.CheckpointStmt:
				walk(s.Target)
				walkStatements(s.Body)
			case *ast.GroupedCheckpointStmt:
				for _, target := range s.Targets {
					walk(target)
				}
				walkStatements(s.Body)
			}
		}
	}
	walk(expr)
	return found
}
