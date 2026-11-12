package parserdesugar

import "elisacore/src/ast"

func SingleThreadStmts(value ast.Expr, name string) []ast.Stmt {
	switch n := value.(type) {
	case *ast.TernaryExpr:
		thenStmts := SingleThreadStmts(n.Value, name)
		elseStmts := SingleThreadStmts(n.Alt, name)
		if len(thenStmts) == 0 {
			thenStmts = []ast.Stmt{&ast.PassStmt{Position: n.Position}}
		}
		return []ast.Stmt{&ast.IfStmt{Position: n.Position, Cond: n.Cond, Then: thenStmts, Else: elseStmts}}
	case *ast.ExprBlock:
		stmts := append([]ast.Stmt(nil), n.Stmts...)
		return append(stmts, SingleThreadStmts(n.Value, name)...)
	default:
		if id, ok := value.(*ast.Ident); ok && id.Name == name {
			return nil // neutral: yields the binding unchanged — erases
		}
		MarkThreadEffect(value)
		return []ast.Stmt{&ast.ExprStmt{Position: value.Pos(), Expr: value}}
	}
}

// threadOnlyStmts converts an all-slots-thread RHS into plain statements: a value-if
// becomes a statement if whose branch bodies are the branches' pre-tail statements
// plus their hoisted thread effects.
func ThreadOnlyStmts(value ast.Expr, hoistsOf func(*ast.TupleExpr) []ast.Stmt) []ast.Stmt {
	switch n := value.(type) {
	case *ast.TupleExpr:
		return hoistsOf(n)
	case *ast.ExprBlock:
		// The block's pre-tail statements execute, then whatever its value position
		// contributes (a tuple's hoisted effects, or a nested value-if's statement form).
		stmts := append([]ast.Stmt(nil), n.Stmts...)
		return append(stmts, ThreadOnlyStmts(n.Value, hoistsOf)...)
	case *ast.TernaryExpr:
		ifStmt := &ast.IfStmt{
			Position: n.Position,
			Cond:     n.Cond,
			Then:     ThreadOnlyStmts(n.Value, hoistsOf),
			Else:     ThreadOnlyStmts(n.Alt, hoistsOf),
		}
		return []ast.Stmt{ifStmt}
	}
	return nil
}
