package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/purereturnshape"
)

type pureReturnCase = purereturnshape.Case

const (
	pureReturnMaxDepth = purereturnshape.MaxDepth
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

func pureReturnExpressionShapeWithinBudget(expr ast.Expr, work *int, depth int) bool {
	return purereturnshape.ExpressionWithinBudget(expr, work, depth)
}

func pureReturnCasesFromStmts(stmts []ast.Stmt, path ast.Expr, subst map[string]ast.Expr, work *int, depth int) ([]pureReturnCase, bool) {
	return purereturnshape.CasesFromStmts(stmts, path, subst, work, depth, substitutePureReturnExpr, wherePredicateIsSideEffectFree)
}

func combinePathCond(path, cond ast.Expr) ast.Expr {
	return purereturnshape.CombinePathCond(path, cond)
}
func pureCasesToExpr(cases []pureReturnCase) ast.Expr { return purereturnshape.CasesToExpr(cases) }
