package machineroots

import "elisacore/src/ast"

func CollectStmtRootIdents(stmt ast.Stmt, out map[string]bool) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		CollectExprRootIdents(s.Expr, out)
	case *ast.AssignStmt:
		CollectExprRootIdents(s.Target, out)
		CollectExprRootIdents(s.Value, out)
	case *ast.AugAssignStmt:
		CollectExprRootIdents(s.Target, out)
		CollectExprRootIdents(s.Value, out)
	case *ast.AsRefAssignStmt:
		CollectExprRootIdents(s.Target, out)
		CollectExprRootIdents(s.Value, out)
	case *ast.VarDeclStmt:
		CollectExprRootIdents(s.Value, out)
	case *ast.LetDestructureStmt:
		CollectExprRootIdents(s.Value, out)
	case *ast.TupleBindStmt:
		CollectExprRootIdents(s.Value, out)
	case *ast.MoveBindStmt:
		CollectExprRootIdents(s.Value, out)
		CollectExprRootIdents(s.Store, out)
	case *ast.DeferStmt:
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.ReturnStmt:
		CollectExprRootIdents(s.Value, out)
	case *ast.IfStmt:
		CollectExprRootIdents(s.Cond, out)
		for _, nested := range s.Then {
			CollectStmtRootIdents(nested, out)
		}
		for _, clause := range s.Elifs {
			CollectExprRootIdents(clause.Cond, out)
			for _, nested := range clause.Body {
				CollectStmtRootIdents(nested, out)
			}
		}
		for _, nested := range s.Else {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.WhileStmt:
		CollectExprRootIdents(s.Cond, out)
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.ForStmt:
		CollectExprRootIdents(s.Start, out)
		CollectExprRootIdents(s.End, out)
		CollectExprRootIdents(s.Step, out)
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.IterForStmt:
		CollectExprRootIdents(s.Source, out)
		CollectExprRootIdents(s.WhereFilter, out)
		CollectExprRootIdents(s.Filter, out)
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.ParallelForStmt:
		CollectExprRootIdents(s.Source, out)
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.MatchStmt:
		CollectExprRootIdents(s.Value, out)
		CollectExprRootIdents(s.Store, out)
		for _, arm := range s.Arms {
			CollectExprRootIdents(arm.Guard, out)
			for _, nested := range arm.Body {
				CollectStmtRootIdents(nested, out)
			}
		}
	case *ast.ExpectPatternStmt:
		CollectExprRootIdents(s.Value, out)
	case *ast.InStoreStmt:
		CollectExprRootIdents(s.Store, out)
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.CanStmt:
		for _, arg := range s.HandlerArgs {
			CollectExprRootIdents(arg, out)
		}
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.ScopeStmt:
		CollectExprRootIdents(s.Guard, out)
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.PoolStmt:
		CollectExprRootIdents(s.Workers, out)
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.LockStmt:
		CollectExprRootIdents(s.Mutex, out)
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.PanicStmt:
		CollectExprRootIdents(s.Message, out)
	case *ast.StaticIfStmt:
		CollectExprRootIdents(s.Cond, out)
		for _, nested := range s.Then {
			CollectStmtRootIdents(nested, out)
		}
		for _, clause := range s.Elifs {
			CollectExprRootIdents(clause.Cond, out)
			for _, nested := range clause.Body {
				CollectStmtRootIdents(nested, out)
			}
		}
		for _, nested := range s.Else {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.StaticErrorStmt:
		CollectExprRootIdents(s.Message, out)
	case *ast.StaticAssertStmt:
		CollectExprRootIdents(s.Cond, out)
		CollectExprRootIdents(s.Message, out)
	case *ast.AssertByStmt:
		CollectExprRootIdents(s.Cond, out)
		for _, nested := range s.Proof {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.ProofBlockStmt:
		CollectExprRootIdents(s.Goal, out)
		for _, nested := range s.Proof {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.ProofUseStmt:
		for _, citation := range s.Citations {
			CollectExprRootIdents(citation, out)
		}
	case *ast.ContractStmt:
		CollectExprRootIdents(s.Cond, out)
		for _, nested := range s.Proof {
			CollectStmtRootIdents(nested, out)
		}
		for _, arg := range s.UsesArgs {
			CollectExprRootIdents(arg, out)
		}
	case *ast.StaticBlockStmt:
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.DiscardStmt:
		CollectExprRootIdents(s.Value, out)
	case *ast.RegionStmt:
		CollectExprRootIdents(s.Capacity, out)
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.PromoteStmt:
		CollectExprRootIdents(s.Value, out)
	case *ast.CheckpointStmt:
		CollectExprRootIdents(s.Target, out)
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	case *ast.GroupedCheckpointStmt:
		for _, target := range s.Targets {
			CollectExprRootIdents(target, out)
		}
		for _, nested := range s.Body {
			CollectStmtRootIdents(nested, out)
		}
	}
}
