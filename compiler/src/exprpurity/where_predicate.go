package exprpurity

import "elisacore/src/ast"

func WherePredicate(expr ast.Expr) bool {
	pure := WherePredicate // alias for recursion
	switch n := expr.(type) {
	case nil, *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.CharLit:
		return true
	case *ast.ParenExpr:
		return pure(n.Inner)
	case *ast.UnaryExpr:
		return pure(n.Operand)
	case *ast.BinaryExpr:
		return pure(n.Left) && pure(n.Right)
	case *ast.FieldExpr:
		// Field access on a pure object is pure (read-only projection, no mutation).
		return pure(n.Object)
	case *ast.IndexExpr:
		return pure(n.Object) && pure(n.Index) && pure(n.Index2) && pure(n.Fallback)
	case *ast.SliceExpr:
		return pure(n.Object) && pure(n.Start) && pure(n.End)
	case *ast.TernaryExpr:
		// `Cond ? Value : Alt` — all three branches are pure reads.
		return pure(n.Cond) && pure(n.Value) && pure(n.Alt)
	case *ast.TupleExpr:
		// Tuple constructor from pure elements is pure (creates a value, no side effects).
		for _, e := range n.Elems {
			if !pure(e) {
				return false
			}
		}
		return true
	case *ast.ListLitExpr:
		// List/set/dict literals are pure when all elements (and keys) are pure.
		// Spreads are still reads, so they don't break purity.
		for _, e := range n.Elems {
			if !pure(e) {
				return false
			}
		}
		for _, k := range n.Keys {
			if !pure(k) {
				return false
			}
		}
		return true
	case *ast.CastExpr:
		// A value-reinterpret cast is pure: it reads the operand and reinterprets it, no writes.
		return pure(n.Operand)
	case *ast.AddrOfExpr:
		// Taking the address of a pure sub-expression doesn't mutate anything.
		return pure(n.Operand)
	case *ast.SpecializeExpr:
		// Generic specialisation is a compile-time rewrite; the value operand is read-only.
		return pure(n.Operand)
	case *ast.SizeofExpr, *ast.AlignofExpr, *ast.OffsetofExpr:
		// Compile-time layout queries — always pure (no runtime reads at all).
		return true
	case *ast.QuantifierExpr:
		// forall/exists — purely logical, no mutation.
		return pure(n.In) && pure(n.Body)
	}
	// Every other kind (CallExpr, AllocExpr, LambdaExpr, MatchExpr, RaiseExpr, etc.) is conservatively
	// treated as potentially impure.
	return false
}
