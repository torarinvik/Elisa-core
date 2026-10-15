package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type pureReturnCase struct {
	Cond ast.Expr
	Expr ast.Expr
}

const (
	pureReturnMaxDepth = 64
	pureReturnMaxWork  = 4096
)

// pureReturnBody extracts a function's pure return expression. Kept for callers that only need the
// expression; it now accepts pure return-path trees (`if`/`elif`/`else`) in addition to a single
// `return`.
func pureReturnBody(decl *ast.FuncDecl) (ast.Expr, bool) {
	return pureReturnExpr(decl)
}

func pureReturnExpr(decl *ast.FuncDecl) (ast.Expr, bool) {
	cases, ok := pureReturnCases(decl)
	if !ok || len(cases) == 0 {
		return nil, false
	}
	return pureCasesToExpr(cases), true
}

func pureReturnCases(decl *ast.FuncDecl) ([]pureReturnCase, bool) {
	if decl == nil || len(decl.Body) == 0 {
		return nil, false
	}
	work := pureReturnMaxWork
	return pureReturnCasesFromStmts(decl.Body, nil, nil, &work, 0)
}

// Preserve analyzed AST identity when no local alias needs substitution. The analyzer's type table
// is keyed by node identity; cloning an otherwise unchanged expression drops fixed-width types and
// can turn wrapping arithmetic into unbounded integer arithmetic in SMT equations.
func substitutePureReturnExpr(expr ast.Expr, subst map[string]ast.Expr, work *int) (ast.Expr, bool) {
	if expr == nil || work == nil {
		return nil, false
	}
	// Even an empty substitution must walk the expression under a bound: returning the original
	// node preserves analyzed AST identity, while the temporary rewrite proves the walk is finite.
	rewritten, ok := substituteLemmaEnsureWithBudget(expr, subst, work, 0)
	if !ok {
		return nil, false
	}
	if len(subst) == 0 {
		return expr, true
	}
	return rewritten, true
}

// pureReturnExpressionShapeWithinBudget preflights the expression forms admitted as local aliases
// before calling the recursive purity predicate. That predicate predates hostile-source budgets,
// so this bounded walk prevents deeply nested initializers from overflowing the Go stack.
func pureReturnExpressionShapeWithinBudget(expr ast.Expr, work *int, depth int) bool {
	if expr == nil {
		return true
	}
	if work == nil || depth >= pureReturnMaxDepth || *work <= 0 {
		return false
	}
	(*work)--
	switch n := expr.(type) {
	case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.BoolLit, *ast.CharLit, *ast.NullLit,
		*ast.SizeofExpr, *ast.AlignofExpr, *ast.OffsetofExpr:
		return true
	case *ast.ParenExpr:
		return pureReturnExpressionShapeWithinBudget(n.Inner, work, depth+1)
	case *ast.UnaryExpr:
		return pureReturnExpressionShapeWithinBudget(n.Operand, work, depth+1)
	case *ast.BinaryExpr:
		return pureReturnExpressionShapeWithinBudget(n.Left, work, depth+1) && pureReturnExpressionShapeWithinBudget(n.Right, work, depth+1)
	case *ast.FieldExpr:
		return pureReturnExpressionShapeWithinBudget(n.Object, work, depth+1)
	case *ast.IndexExpr:
		return pureReturnExpressionShapeWithinBudget(n.Object, work, depth+1) && pureReturnExpressionShapeWithinBudget(n.Index, work, depth+1) &&
			pureReturnExpressionShapeWithinBudget(n.Index2, work, depth+1) && pureReturnExpressionShapeWithinBudget(n.Fallback, work, depth+1)
	case *ast.SliceExpr:
		return pureReturnExpressionShapeWithinBudget(n.Object, work, depth+1) && pureReturnExpressionShapeWithinBudget(n.Start, work, depth+1) && pureReturnExpressionShapeWithinBudget(n.End, work, depth+1)
	case *ast.TernaryExpr:
		return pureReturnExpressionShapeWithinBudget(n.Cond, work, depth+1) && pureReturnExpressionShapeWithinBudget(n.Value, work, depth+1) && pureReturnExpressionShapeWithinBudget(n.Alt, work, depth+1)
	case *ast.CallExpr:
		if !pureReturnExpressionShapeWithinBudget(n.Func, work, depth+1) {
			return false
		}
		for _, arg := range n.Args {
			if !pureReturnExpressionShapeWithinBudget(arg, work, depth+1) {
				return false
			}
		}
		return true
	case *ast.MatchExpr:
		if n.Store != nil || !pureReturnExpressionShapeWithinBudget(n.Value, work, depth+1) {
			return false
		}
		for _, arm := range n.Arms {
			if !lemmaScalarMatchPattern(arm.Pattern) || arm.Guard != nil || len(arm.Body) != 1 {
				return false
			}
			tail, ok := arm.Body[0].(*ast.ExprStmt)
			if !ok || !pureReturnExpressionShapeWithinBudget(tail.Expr, work, depth+1) {
				return false
			}
		}
		return true
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			if !pureReturnExpressionShapeWithinBudget(elem, work, depth+1) {
				return false
			}
		}
		return true
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			if !pureReturnExpressionShapeWithinBudget(elem, work, depth+1) {
				return false
			}
		}
		for _, key := range n.Keys {
			if !pureReturnExpressionShapeWithinBudget(key, work, depth+1) {
				return false
			}
		}
		return true
	case *ast.CastExpr:
		return pureReturnExpressionShapeWithinBudget(n.Operand, work, depth+1)
	case *ast.AddrOfExpr:
		return pureReturnExpressionShapeWithinBudget(n.Operand, work, depth+1)
	case *ast.SpecializeExpr:
		return pureReturnExpressionShapeWithinBudget(n.Operand, work, depth+1)
	case *ast.QuantifierExpr:
		return pureReturnExpressionShapeWithinBudget(n.In, work, depth+1) && pureReturnExpressionShapeWithinBudget(n.Body, work, depth+1)
	default:
		return false
	}
}

// pureReturnCasesFromStmts lowers the return-tree subset to an expression. Immutable, owner-free
// locals with syntactically side-effect-free initializers are inlined into later guards and return
// expressions. Calls in local initializers are declined because purity cannot be decided here;
// callers that reason about the resulting expression still apply their own purity and totality gates.
// All other statements remain a conservative decline.
func pureReturnCasesFromStmts(stmts []ast.Stmt, path ast.Expr, subst map[string]ast.Expr, work *int, depth int) ([]pureReturnCase, bool) {
	if len(stmts) == 0 || work == nil || depth >= pureReturnMaxDepth || *work <= 0 {
		return nil, false
	}
	(*work)--
	if local, ok := stmts[0].(*ast.VarDeclStmt); ok {
		if local == nil || local.Name == "" || local.Mutable || local.Owner != nil || local.Value == nil ||
			len(stmts) == 1 || !pureReturnExpressionShapeWithinBudget(local.Value, work, 0) || !wherePredicateIsSideEffectFree(local.Value) {
			return nil, false
		}
		value, ok := substitutePureReturnExpr(local.Value, subst, work)
		if !ok {
			return nil, false
		}
		nextSubst := make(map[string]ast.Expr, len(subst)+1)
		for name, expr := range subst {
			nextSubst[name] = expr
		}
		nextSubst[local.Name] = value
		return pureReturnCasesFromStmts(stmts[1:], path, nextSubst, work, depth+1)
	}
	if len(stmts) > 1 {
		first, ok := stmts[0].(*ast.IfStmt)
		if !ok || first == nil || len(first.Else) != 0 {
			return nil, false
		}
		cond, ok := substitutePureReturnExpr(first.Cond, subst, work)
		if !ok {
			return nil, false
		}
		thenCond := combinePathCond(path, cond)
		thenCases, ok := pureReturnCasesFromStmts(first.Then, thenCond, subst, work, depth+1)
		if !ok {
			return nil, false
		}
		negatedPrior := ast.Expr(&ast.UnaryExpr{Position: cond.Pos(), Op: lexer.TOKEN_NOT, Operand: cond})
		var out []pureReturnCase
		out = append(out, thenCases...)
		for _, elif := range first.Elifs {
			if *work <= 0 {
				return nil, false
			}
			(*work)--
			elifCond, ok := substitutePureReturnExpr(elif.Cond, subst, work)
			if !ok {
				return nil, false
			}
			branchCond := combinePathCond(path, &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: elifCond})
			cases, ok := pureReturnCasesFromStmts(elif.Body, branchCond, subst, work, depth+1)
			if !ok {
				return nil, false
			}
			out = append(out, cases...)
			negatedPrior = &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: &ast.UnaryExpr{Position: elifCond.Pos(), Op: lexer.TOKEN_NOT, Operand: elifCond}}
		}
		fallthroughCases, ok := pureReturnCasesFromStmts(stmts[1:], combinePathCond(path, negatedPrior), subst, work, depth+1)
		if !ok {
			return nil, false
		}
		out = append(out, fallthroughCases...)
		return out, true
	}
	switch n := stmts[0].(type) {
	case *ast.ReturnStmt:
		if n == nil || n.Value == nil {
			return nil, false
		}
		value, ok := substitutePureReturnExpr(n.Value, subst, work)
		if !ok {
			return nil, false
		}
		return []pureReturnCase{{Cond: path, Expr: value}}, true
	case *ast.IfStmt:
		if n == nil || n.Cond == nil {
			return nil, false
		}
		cond, ok := substitutePureReturnExpr(n.Cond, subst, work)
		if !ok {
			return nil, false
		}
		var out []pureReturnCase
		thenCond := combinePathCond(path, cond)
		thenCases, ok := pureReturnCasesFromStmts(n.Then, thenCond, subst, work, depth+1)
		if !ok {
			return nil, false
		}
		out = append(out, thenCases...)
		negatedPrior := ast.Expr(&ast.UnaryExpr{Position: cond.Pos(), Op: lexer.TOKEN_NOT, Operand: cond})
		for _, elif := range n.Elifs {
			if *work <= 0 {
				return nil, false
			}
			(*work)--
			if elif.Cond == nil {
				return nil, false
			}
			elifCond, ok := substitutePureReturnExpr(elif.Cond, subst, work)
			if !ok {
				return nil, false
			}
			branchCond := combinePathCond(path, &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: elifCond})
			cases, ok := pureReturnCasesFromStmts(elif.Body, branchCond, subst, work, depth+1)
			if !ok {
				return nil, false
			}
			out = append(out, cases...)
			negatedPrior = &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: &ast.UnaryExpr{Position: elifCond.Pos(), Op: lexer.TOKEN_NOT, Operand: elifCond}}
		}
		elseCond := combinePathCond(path, negatedPrior)
		elseCases, ok := pureReturnCasesFromStmts(n.Else, elseCond, subst, work, depth+1)
		if !ok {
			return nil, false
		}
		out = append(out, elseCases...)
		return out, true
	default:
		return nil, false
	}
}

func combinePathCond(path, cond ast.Expr) ast.Expr {
	if path == nil {
		return cond
	}
	return &ast.BinaryExpr{Position: cond.Pos(), Op: lexer.TOKEN_AND, Left: path, Right: cond}
}

func pureCasesToExpr(cases []pureReturnCase) ast.Expr {
	if len(cases) == 0 {
		return nil
	}
	fallback := cases[len(cases)-1].Expr
	for i := len(cases) - 2; i >= 0; i-- {
		cond := cases[i].Cond
		if cond == nil {
			fallback = cases[i].Expr
			continue
		}
		fallback = &ast.TernaryExpr{Position: cases[i].Expr.Pos(), Cond: cond, Value: cases[i].Expr, Alt: fallback}
	}
	return fallback
}
