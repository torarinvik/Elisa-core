package machineroots

import "elisacore/src/ast"

func AssignRootIdent(target ast.Expr) string {
	for {
		switch t := target.(type) {
		case *ast.Ident:
			return t.Name
		case *ast.FieldExpr:
			target = t.Object
		case *ast.IndexExpr:
			target = t.Object
		case *ast.ParenExpr:
			target = t.Inner
		default:
			return ""
		}
	}
}

// collectExprRootIdents gathers every identifier that roots a value chain in an
// expression (`lexer.current_char()` → lexer). Used for the driven-resource whitelist.
func CollectExprRootIdents(expr ast.Expr, out map[string]bool) {
	switch e := expr.(type) {
	case nil:
	case *ast.Ident:
		out[e.Name] = true
	case *ast.FieldExpr:
		CollectExprRootIdents(e.Object, out)
	case *ast.CallExpr:
		// A plain-ident callee is a FUNCTION name (`at_end(scanner)`), not a value root;
		// a FieldExpr callee's object chain is a method receiver and does root a value.
		if _, plainFn := e.Func.(*ast.Ident); !plainFn {
			CollectExprRootIdents(e.Func, out)
		}
		for _, a := range e.Args {
			CollectExprRootIdents(a, out)
		}
	case *ast.IndexExpr:
		CollectExprRootIdents(e.Object, out)
		CollectExprRootIdents(e.Index, out)
		CollectExprRootIdents(e.Index2, out)
		CollectExprRootIdents(e.Fallback, out)
	case *ast.CastExpr:
		// A postfix value cast/ctor (`box.data[i].char()`) is a CastExpr, not a method
		// call — the driven resource is rooted in its operand (`box`), so descend.
		CollectExprRootIdents(e.Operand, out)
	case *ast.BinaryExpr:
		CollectExprRootIdents(e.Left, out)
		CollectExprRootIdents(e.Right, out)
	case *ast.UnaryExpr:
		CollectExprRootIdents(e.Operand, out)
	case *ast.ParenExpr:
		CollectExprRootIdents(e.Inner, out)
	case *ast.SliceExpr:
		CollectExprRootIdents(e.Object, out)
		CollectExprRootIdents(e.Start, out)
		CollectExprRootIdents(e.End, out)
	case *ast.ListLitExpr:
		for _, elem := range e.Elems {
			CollectExprRootIdents(elem, out)
		}
		for _, key := range e.Keys {
			CollectExprRootIdents(key, out)
		}
		CollectExprRootIdents(e.Owner, out)
	case *ast.MembershipRangeExpr:
		CollectExprRootIdents(e.Start, out)
		CollectExprRootIdents(e.End, out)
	case *ast.ListComprehensionExpr:
		CollectExprRootIdents(e.Value, out)
		CollectExprRootIdents(e.Source, out)
		CollectExprRootIdents(e.RangeEnd, out)
		CollectExprRootIdents(e.RangeStep, out)
		CollectExprRootIdents(e.Filter, out)
		CollectExprRootIdents(e.Owner, out)
		CollectExprRootIdents(e.Key, out)
		CollectExprRootIdents(e.LoweredParallel, out)
		for _, binding := range e.Bindings {
			CollectStmtRootIdents(binding, out)
		}
	case *ast.QueryExpr:
		CollectExprRootIdents(e.Source, out)
		CollectExprRootIdents(e.Filter, out)
		CollectExprRootIdents(e.Projection, out)
		CollectExprRootIdents(e.Owner, out)
	case *ast.TernaryExpr:
		CollectExprRootIdents(e.Value, out)
		CollectExprRootIdents(e.Cond, out)
		CollectExprRootIdents(e.Alt, out)
	case *ast.AddrOfExpr:
		CollectExprRootIdents(e.Operand, out)
	case *ast.SpecializeExpr:
		CollectExprRootIdents(e.Operand, out)
	case *ast.StructLitExpr:
		for _, arg := range e.Args {
			CollectExprRootIdents(arg, out)
		}
		for _, spread := range e.Spreads {
			CollectExprRootIdents(spread, out)
		}
	case *ast.RecordUpdateExpr:
		CollectExprRootIdents(e.Base, out)
		for _, arg := range e.Args {
			CollectExprRootIdents(arg, out)
		}
	case *ast.TupleExpr:
		for _, elem := range e.Elems {
			CollectExprRootIdents(elem, out)
		}
	case *ast.OptionalBindExpr:
		CollectExprRootIdents(e.Value, out)
	case *ast.AllocExpr:
		CollectExprRootIdents(e.Owner, out)
		CollectExprRootIdents(e.Value, out)
	case *ast.CanExpr:
		CollectExprRootIdents(e.Expr, out)
	case *ast.RaiseExpr:
		CollectExprRootIdents(e.Error, out)
	case *ast.IsPatternExpr:
		for _, target := range e.Targets {
			CollectExprRootIdents(target, out)
		}
	case *ast.IsAliasExpr:
		CollectExprRootIdents(e.Target, out)
	case *ast.ExprBlock:
		for _, stmt := range e.Stmts {
			CollectStmtRootIdents(stmt, out)
		}
		CollectExprRootIdents(e.Value, out)
	case *ast.LambdaExpr:
		for _, stmt := range e.Body {
			CollectStmtRootIdents(stmt, out)
		}
		CollectExprRootIdents(e.BodyExpr, out)
	case *ast.FoldExpr:
		CollectExprRootIdents(e.Value, out)
		for _, arm := range e.Arms {
			CollectExprRootIdents(arm.Guard, out)
			for _, stmt := range arm.Body {
				CollectStmtRootIdents(stmt, out)
			}
		}
	case *ast.EmitExpr:
		CollectExprRootIdents(e.Value, out)
	case *ast.TryExpr:
		CollectExprRootIdents(e.Value, out)
		CollectExprRootIdents(e.Fallback, out)
		if e.Recovery != nil {
			CollectExprRootIdents(e.Recovery.Value, out)
			for _, stmt := range e.Recovery.Body {
				CollectStmtRootIdents(stmt, out)
			}
		}
	case *ast.GetExpr:
		CollectExprRootIdents(e.Value, out)
		CollectExprRootIdents(e.Fallback, out)
		if e.Recovery != nil {
			CollectExprRootIdents(e.Recovery.Value, out)
			for _, stmt := range e.Recovery.Body {
				CollectStmtRootIdents(stmt, out)
			}
		}
	case *ast.UnwrapElseExpr:
		CollectExprRootIdents(e.Value, out)
		CollectExprRootIdents(e.Fallback, out)
		if e.Recovery != nil {
			CollectExprRootIdents(e.Recovery.Value, out)
			for _, stmt := range e.Recovery.Body {
				CollectStmtRootIdents(stmt, out)
			}
		}
	case *ast.CatchExpr:
		CollectExprRootIdents(e.Value, out)
		for _, stmt := range e.Success.Body {
			CollectStmtRootIdents(stmt, out)
		}
		for _, arm := range e.Arms {
			for _, stmt := range arm.Body {
				CollectStmtRootIdents(stmt, out)
			}
		}
	case *ast.MatchExpr:
		CollectExprRootIdents(e.Value, out)
		CollectExprRootIdents(e.Store, out)
		for _, arm := range e.Arms {
			CollectExprRootIdents(arm.Guard, out)
			for _, stmt := range arm.Body {
				CollectStmtRootIdents(stmt, out)
			}
		}
	}
}
