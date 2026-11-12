package astscan

import "elisacore/src/ast"

func ExprIdentNames(expr ast.Expr) []string {
	seen := map[string]bool{}
	var out []string
	VisitExprIdentNames(expr, func(name string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	})
	return out
}

// VisitExprIdentNames walks the same syntax as ExprIdentNames without requiring
// an intermediate deduplicated slice when the caller already has a set.
func VisitExprIdentNames(expr ast.Expr, visit func(string)) {
	var walk func(ast.Expr)
	walkRecovery := func(recovery *ast.RecoveryClause) {
		if recovery == nil {
			return
		}
		walk(recovery.Value)
		WalkStatements(recovery.Body, func(value any) {
			if ident, ok := value.(*ast.Ident); ok && ident != nil {
				visit(ident.Name)
			}
		}, make(map[uintptr]bool))
	}
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case nil:
		case *ast.Ident:
			visit(n.Name)
		case *ast.ParenExpr:
			walk(n.Inner)
		case *ast.MoveExpr:
			walk(n.Operand)
		case *ast.RaiseExpr:
			walk(n.Error)
		case *ast.CanExpr:
			walk(n.Expr)
		case *ast.AllocExpr:
			walk(n.Owner)
			walk(n.Value)
		case *ast.OptionalBindExpr:
			walk(n.Value)
		case *ast.UnaryExpr:
			walk(n.Operand)
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.FieldExpr:
			walk(n.Object)
		case *ast.IndexExpr:
			walk(n.Object)
			walk(n.Index)
			walk(n.Index2)
			walk(n.Fallback)
		case *ast.SliceExpr:
			walk(n.Object)
			walk(n.Start)
			walk(n.End)
		case *ast.CallExpr:
			walk(n.Func)
			walk(n.SafeReceiver)
			for _, arg := range n.Args {
				walk(arg)
			}
		case *ast.TernaryExpr:
			walk(n.Cond)
			walk(n.Value)
			walk(n.Alt)
		case *ast.TupleExpr:
			for _, elem := range n.Elems {
				walk(elem)
			}
		case *ast.ListLitExpr:
			for _, elem := range n.Elems {
				walk(elem)
			}
			for _, k := range n.Keys {
				walk(k)
			}
			walk(n.Owner)
		case *ast.CastExpr:
			walk(n.Operand)
		case *ast.AddrOfExpr:
			walk(n.Operand)
		case *ast.SpecializeExpr:
			walk(n.Operand)
		case *ast.QuantifierExpr:
			walk(n.In)
			walk(n.Body)
		case *ast.MembershipRangeExpr:
			walk(n.Start)
			walk(n.End)
		case *ast.RecordUpdateExpr:
			walk(n.Base)
			for _, arg := range n.Args {
				walk(arg)
			}
		case *ast.StructLitExpr:
			walk(n.CopyBase)
			for _, spread := range n.Spreads {
				walk(spread)
			}
			for _, arg := range n.Args {
				walk(arg)
			}
		case *ast.UnwrapElseExpr:
			walk(n.Value)
			walk(n.Fallback)
			walkRecovery(n.Recovery)
		case *ast.GetExpr:
			walk(n.Value)
			walk(n.Fallback)
			walkRecovery(n.Recovery)
		case *ast.TryExpr:
			walk(n.Value)
			walk(n.Fallback)
			walkRecovery(n.Recovery)
		}
	}
	walk(expr)
}
