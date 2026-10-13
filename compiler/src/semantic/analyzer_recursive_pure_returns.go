package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type pureReturnCase struct {
	Cond ast.Expr
	Expr ast.Expr
}

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
	return pureReturnCasesFromStmts(decl.Body, nil, nil)
}

// Preserve analyzed AST identity when no local alias needs substitution. The analyzer's type table
// is keyed by node identity; cloning an otherwise unchanged expression drops fixed-width types and
// can turn wrapping arithmetic into unbounded integer arithmetic in SMT equations.
func substitutePureReturnExpr(expr ast.Expr, subst map[string]ast.Expr) (ast.Expr, bool) {
	if len(subst) == 0 {
		return expr, expr != nil
	}
	return substituteLemmaEnsure(expr, subst)
}

// pureReturnCasesFromStmts lowers the return-tree subset to an expression. Immutable, owner-free
// locals with syntactically side-effect-free initializers are inlined into later guards and return
// expressions. Calls in local initializers are declined because purity cannot be decided here;
// callers that reason about the resulting expression still apply their own purity and totality gates.
// All other statements remain a conservative decline.
func pureReturnCasesFromStmts(stmts []ast.Stmt, path ast.Expr, subst map[string]ast.Expr) ([]pureReturnCase, bool) {
	if len(stmts) == 0 {
		return nil, false
	}
	if local, ok := stmts[0].(*ast.VarDeclStmt); ok {
		if local == nil || local.Name == "" || local.Mutable || local.Owner != nil || local.Value == nil ||
			len(stmts) == 1 || !wherePredicateIsSideEffectFree(local.Value) {
			return nil, false
		}
		value, ok := substitutePureReturnExpr(local.Value, subst)
		if !ok {
			return nil, false
		}
		nextSubst := make(map[string]ast.Expr, len(subst)+1)
		for name, expr := range subst {
			nextSubst[name] = expr
		}
		nextSubst[local.Name] = value
		return pureReturnCasesFromStmts(stmts[1:], path, nextSubst)
	}
	if len(stmts) > 1 {
		first, ok := stmts[0].(*ast.IfStmt)
		if !ok || first == nil || len(first.Else) != 0 {
			return nil, false
		}
		cond, ok := substitutePureReturnExpr(first.Cond, subst)
		if !ok {
			return nil, false
		}
		thenCond := combinePathCond(path, cond)
		thenCases, ok := pureReturnCasesFromStmts(first.Then, thenCond, subst)
		if !ok {
			return nil, false
		}
		negatedPrior := ast.Expr(&ast.UnaryExpr{Position: cond.Pos(), Op: lexer.TOKEN_NOT, Operand: cond})
		var out []pureReturnCase
		out = append(out, thenCases...)
		for _, elif := range first.Elifs {
			elifCond, ok := substitutePureReturnExpr(elif.Cond, subst)
			if !ok {
				return nil, false
			}
			branchCond := combinePathCond(path, &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: elifCond})
			cases, ok := pureReturnCasesFromStmts(elif.Body, branchCond, subst)
			if !ok {
				return nil, false
			}
			out = append(out, cases...)
			negatedPrior = &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: &ast.UnaryExpr{Position: elifCond.Pos(), Op: lexer.TOKEN_NOT, Operand: elifCond}}
		}
		fallthroughCases, ok := pureReturnCasesFromStmts(stmts[1:], combinePathCond(path, negatedPrior), subst)
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
		value, ok := substitutePureReturnExpr(n.Value, subst)
		if !ok {
			return nil, false
		}
		return []pureReturnCase{{Cond: path, Expr: value}}, true
	case *ast.IfStmt:
		if n == nil || n.Cond == nil {
			return nil, false
		}
		cond, ok := substitutePureReturnExpr(n.Cond, subst)
		if !ok {
			return nil, false
		}
		var out []pureReturnCase
		thenCond := combinePathCond(path, cond)
		thenCases, ok := pureReturnCasesFromStmts(n.Then, thenCond, subst)
		if !ok {
			return nil, false
		}
		out = append(out, thenCases...)
		negatedPrior := ast.Expr(&ast.UnaryExpr{Position: cond.Pos(), Op: lexer.TOKEN_NOT, Operand: cond})
		for _, elif := range n.Elifs {
			if elif.Cond == nil {
				return nil, false
			}
			elifCond, ok := substitutePureReturnExpr(elif.Cond, subst)
			if !ok {
				return nil, false
			}
			branchCond := combinePathCond(path, &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: elifCond})
			cases, ok := pureReturnCasesFromStmts(elif.Body, branchCond, subst)
			if !ok {
				return nil, false
			}
			out = append(out, cases...)
			negatedPrior = &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: &ast.UnaryExpr{Position: elifCond.Pos(), Op: lexer.TOKEN_NOT, Operand: elifCond}}
		}
		elseCond := combinePathCond(path, negatedPrior)
		elseCases, ok := pureReturnCasesFromStmts(n.Else, elseCond, subst)
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
