package machineroots

import "elisacore/src/ast"

func CollectMachineCallArgRoots(stmt ast.Stmt, overRoots, out map[string]bool) {
	var visit func(ast.Expr)
	var visitStatements func([]ast.Stmt)
	visitStatements = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.ExprStmt:
				visit(s.Expr)
			case *ast.AssignStmt:
				visit(s.Target)
				visit(s.Value)
			case *ast.AugAssignStmt:
				visit(s.Target)
				visit(s.Value)
			case *ast.AsRefAssignStmt:
				visit(s.Target)
				visit(s.Value)
			case *ast.VarDeclStmt:
				visit(s.Value)
			case *ast.LetDestructureStmt:
				visit(s.Value)
			case *ast.TupleBindStmt:
				visit(s.Value)
			case *ast.MoveBindStmt:
				visit(s.Value)
				visit(s.Store)
			case *ast.DeferStmt:
				visitStatements(s.Body)
			case *ast.ReturnStmt:
				visit(s.Value)
			case *ast.IfStmt:
				visit(s.Cond)
				visitStatements(s.Then)
				for _, clause := range s.Elifs {
					visit(clause.Cond)
					visitStatements(clause.Body)
				}
				visitStatements(s.Else)
			case *ast.WhileStmt:
				visit(s.Cond)
				visitStatements(s.Body)
			case *ast.ForStmt:
				visit(s.Start)
				visit(s.End)
				visit(s.Step)
				visitStatements(s.Body)
			case *ast.IterForStmt:
				visit(s.Source)
				visit(s.WhereFilter)
				visit(s.Filter)
				visitStatements(s.Body)
			case *ast.ParallelForStmt:
				visit(s.Source)
				visitStatements(s.Body)
			case *ast.MatchStmt:
				visit(s.Value)
				visit(s.Store)
				for _, arm := range s.Arms {
					visit(arm.Guard)
					visitStatements(arm.Body)
				}
			case *ast.ExpectPatternStmt:
				visit(s.Value)
			case *ast.InStoreStmt:
				visit(s.Store)
				visitStatements(s.Body)
			case *ast.CanStmt:
				for _, arg := range s.HandlerArgs {
					visit(arg)
				}
				visitStatements(s.Body)
			case *ast.ScopeStmt:
				visit(s.Guard)
				visitStatements(s.Body)
			case *ast.PoolStmt:
				visit(s.Workers)
				visitStatements(s.Body)
			case *ast.LockStmt:
				visit(s.Mutex)
				visitStatements(s.Body)
			case *ast.PanicStmt:
				visit(s.Message)
			case *ast.StaticIfStmt:
				visit(s.Cond)
				visitStatements(s.Then)
				for _, clause := range s.Elifs {
					visit(clause.Cond)
					visitStatements(clause.Body)
				}
				visitStatements(s.Else)
			case *ast.StaticErrorStmt:
				visit(s.Message)
			case *ast.StaticAssertStmt:
				visit(s.Cond)
				visit(s.Message)
			case *ast.AssertByStmt:
				visit(s.Cond)
				visitStatements(s.Proof)
			case *ast.ProofBlockStmt:
				visit(s.Goal)
				visitStatements(s.Proof)
			case *ast.ProofUseStmt:
				for _, citation := range s.Citations {
					visit(citation)
				}
			case *ast.ContractStmt:
				visit(s.Cond)
				visitStatements(s.Proof)
				for _, arg := range s.UsesArgs {
					visit(arg)
				}
			case *ast.StaticBlockStmt:
				visitStatements(s.Body)
			case *ast.DiscardStmt:
				visit(s.Value)
			case *ast.RegionStmt:
				visit(s.Capacity)
				visitStatements(s.Body)
			case *ast.PromoteStmt:
				visit(s.Value)
			case *ast.CheckpointStmt:
				visit(s.Target)
				visitStatements(s.Body)
			case *ast.GroupedCheckpointStmt:
				for _, target := range s.Targets {
					visit(target)
				}
				visitStatements(s.Body)
			}
		}
	}
	visit = func(e ast.Expr) {
		if e == nil {
			return
		}
		switch n := e.(type) {
		case *ast.CallExpr:
			roots := map[string]bool{}
			CollectExprRootIdents(n, roots)
			for r := range roots {
				if overRoots[r] {
					out[r] = true
				}
			}
		case *ast.BinaryExpr:
			visit(n.Left)
			visit(n.Right)
		case *ast.UnaryExpr:
			visit(n.Operand)
		case *ast.ParenExpr:
			visit(n.Inner)
		case *ast.FieldExpr:
			visit(n.Object)
		case *ast.IndexExpr:
			visit(n.Object)
			visit(n.Index)
			visit(n.Index2)
			visit(n.Fallback)
		case *ast.SliceExpr:
			visit(n.Object)
			visit(n.Start)
			visit(n.End)
		case *ast.CastExpr:
			visit(n.Operand)
		case *ast.ListLitExpr:
			for _, elem := range n.Elems {
				visit(elem)
			}
			for _, key := range n.Keys {
				visit(key)
			}
			visit(n.Owner)
		case *ast.MembershipRangeExpr:
			visit(n.Start)
			visit(n.End)
		case *ast.ListComprehensionExpr:
			visit(n.Value)
			visit(n.Source)
			visit(n.RangeEnd)
			visit(n.RangeStep)
			visit(n.Filter)
			visit(n.Owner)
			visit(n.Key)
			visit(n.LoweredParallel)
			visitStatements(n.Bindings)
		case *ast.QueryExpr:
			visit(n.Source)
			visit(n.Filter)
			visit(n.Projection)
			visit(n.Owner)
		case *ast.TernaryExpr:
			visit(n.Value)
			visit(n.Cond)
			visit(n.Alt)
		case *ast.AddrOfExpr:
			visit(n.Operand)
		case *ast.SpecializeExpr:
			visit(n.Operand)
		case *ast.StructLitExpr:
			for _, arg := range n.Args {
				visit(arg)
			}
			for _, spread := range n.Spreads {
				visit(spread)
			}
		case *ast.RecordUpdateExpr:
			visit(n.Base)
			for _, arg := range n.Args {
				visit(arg)
			}
		case *ast.TupleExpr:
			for _, elem := range n.Elems {
				visit(elem)
			}
		case *ast.OptionalBindExpr:
			visit(n.Value)
		case *ast.AllocExpr:
			visit(n.Owner)
			visit(n.Value)
		case *ast.CanExpr:
			visit(n.Expr)
		case *ast.RaiseExpr:
			visit(n.Error)
		case *ast.IsPatternExpr:
			for _, target := range n.Targets {
				visit(target)
			}
		case *ast.IsAliasExpr:
			visit(n.Target)
		case *ast.ExprBlock:
			visitStatements(n.Stmts)
			visit(n.Value)
		case *ast.LambdaExpr:
			visitStatements(n.Body)
			visit(n.BodyExpr)
		case *ast.FoldExpr:
			visit(n.Value)
			for _, arm := range n.Arms {
				visit(arm.Guard)
				visitStatements(arm.Body)
			}
		case *ast.EmitExpr:
			visit(n.Value)
		case *ast.TryExpr:
			visit(n.Value)
			visit(n.Fallback)
			if n.Recovery != nil {
				visit(n.Recovery.Value)
				visitStatements(n.Recovery.Body)
			}
		case *ast.GetExpr:
			visit(n.Value)
			visit(n.Fallback)
			if n.Recovery != nil {
				visit(n.Recovery.Value)
				visitStatements(n.Recovery.Body)
			}
		case *ast.UnwrapElseExpr:
			visit(n.Value)
			visit(n.Fallback)
			if n.Recovery != nil {
				visit(n.Recovery.Value)
				visitStatements(n.Recovery.Body)
			}
		case *ast.CatchExpr:
			visit(n.Value)
			visitStatements(n.Success.Body)
			for _, arm := range n.Arms {
				visitStatements(arm.Body)
			}
		case *ast.MatchExpr:
			visit(n.Value)
			visit(n.Store)
			for _, arm := range n.Arms {
				visit(arm.Guard)
				visitStatements(arm.Body)
			}
		}
	}
	// Enter through the same complete statement dispatcher used for nested bodies. The
	// machine-arm validator now accepts compound and as-ref assignments, and routing only
	// plain assignments here would miss calls in their targets or values when the statement
	// is at arm level.
	visitStatements([]ast.Stmt{stmt})
}
