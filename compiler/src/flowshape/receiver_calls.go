package flowshape

import "elisacore/src/ast"

func StmtsCallOnNames(stmts []ast.Stmt, names map[string]bool) bool {
	for _, stmt := range stmts {
		if flowStmtCallOnNames(stmt, names) {
			return true
		}
	}
	return false
}

func flowStmtCallOnNames(stmt ast.Stmt, names map[string]bool) bool {
	switch n := stmt.(type) {
	case *ast.ExprStmt:
		return flowExprCallOnNames(n.Expr, names)
	case *ast.AssignStmt:
		return flowExprCallOnNames(n.Value, names) || flowExprCallOnNames(n.Target, names)
	case *ast.AugAssignStmt:
		return flowExprCallOnNames(n.Value, names)
	case *ast.VarDeclStmt:
		return flowExprCallOnNames(n.Value, names)
	case *ast.TupleBindStmt:
		return flowExprCallOnNames(n.Value, names)
	case *ast.ReturnStmt:
		return flowExprCallOnNames(n.Value, names)
	case *ast.IfStmt:
		if flowExprCallOnNames(n.Cond, names) || StmtsCallOnNames(n.Then, names) || StmtsCallOnNames(n.Else, names) {
			return true
		}
		for _, elif := range n.Elifs {
			if flowExprCallOnNames(elif.Cond, names) || StmtsCallOnNames(elif.Body, names) {
				return true
			}
		}
	case *ast.MatchStmt:
		if flowExprCallOnNames(n.Value, names) {
			return true
		}
		for _, arm := range n.Arms {
			if StmtsCallOnNames(arm.Body, names) {
				return true
			}
		}
	case *ast.ScopeStmt:
		return StmtsCallOnNames(n.Body, names)
	case *ast.CanStmt:
		return StmtsCallOnNames(n.Body, names)
	case *ast.InStoreStmt:
		return StmtsCallOnNames(n.Body, names)
	case *ast.RegionStmt:
		return StmtsCallOnNames(n.Body, names)
	}
	return false
}

// flowExprCallOnNames reports whether the expression contains a call whose receiver root is one of
// the names, or (conservatively) an ExprBlock in whose statements such a call hides — the value
// block a thread-slot advance is hoisted into.
func flowExprCallOnNames(expr ast.Expr, names map[string]bool) bool {
	switch e := expr.(type) {
	case nil:
		return false
	case *ast.CallExpr:
		if _, receiver := CallNameAndReceiver(e); receiver != "" && names[receiver] {
			return true
		}
		if flowExprCallOnNames(e.Func, names) || flowExprCallOnNames(e.SafeReceiver, names) {
			return true
		}
		for _, arg := range e.Args {
			if flowExprCallOnNames(arg, names) {
				return true
			}
		}
	case *ast.FieldExpr:
		return flowExprCallOnNames(e.Object, names)
	case *ast.BinaryExpr:
		return flowExprCallOnNames(e.Left, names) || flowExprCallOnNames(e.Right, names)
	case *ast.UnaryExpr:
		return flowExprCallOnNames(e.Operand, names)
	case *ast.ParenExpr:
		return flowExprCallOnNames(e.Inner, names)
	case *ast.MoveExpr:
		return flowExprCallOnNames(e.Operand, names)
	case *ast.ExprBlock:
		return StmtsCallOnNames(e.Stmts, names) || flowExprCallOnNames(e.Value, names)
	}
	return false
}

// CallNameAndReceiver extracts a call's method name and, if it is a method/UFCS call on a bare
// binding, that receiver name. Handles both `recv.method()` (FieldExpr callee) and the
// UFCS/lowered `method(recv)` form (Ident callee with a SafeReceiver or leading Ident arg).
func CallNameAndReceiver(call *ast.CallExpr) (string, string) {
	switch fn := call.Func.(type) {
	case *ast.FieldExpr:
		return fn.Field, identNameOf(fn.Object)
	case *ast.Ident:
		receiver := identNameOf(call.SafeReceiver)
		if receiver == "" && len(call.Args) > 0 {
			receiver = identNameOf(call.Args[0])
		}
		return fn.Name, receiver
	}
	return "", ""
}
