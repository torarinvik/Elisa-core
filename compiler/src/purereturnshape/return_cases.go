package purereturnshape

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

type Case struct {
	Cond ast.Expr
	Expr ast.Expr
}

const MaxDepth = 64

// CasesFromStmts lowers the return-tree subset to an expression. Immutable, owner-free
// locals with syntactically side-effect-free initializers are inlined into later guards and return
// expressions. Calls in local initializers are declined because purity cannot be decided here;
// callers that reason about the resulting expression still apply their own purity and totality gates.
// All other statements remain a conservative decline.
func CasesFromStmts(stmts []ast.Stmt, path ast.Expr, subst map[string]ast.Expr, work *int, depth int, substitute func(ast.Expr, map[string]ast.Expr, *int) (ast.Expr, bool), sideEffectFree func(ast.Expr) bool) ([]Case, bool) {
	if len(stmts) == 0 || work == nil || depth >= MaxDepth || *work <= 0 {
		return nil, false
	}
	(*work)--
	if local, ok := stmts[0].(*ast.VarDeclStmt); ok {
		if local == nil || local.Name == "" || local.Mutable || local.Owner != nil || local.Value == nil ||
			len(stmts) == 1 || !ExpressionWithinBudget(local.Value, work, 0) || !sideEffectFree(local.Value) {
			return nil, false
		}
		value, ok := substitute(local.Value, subst, work)
		if !ok {
			return nil, false
		}
		nextSubst := make(map[string]ast.Expr, len(subst)+1)
		for name, expr := range subst {
			nextSubst[name] = expr
		}
		nextSubst[local.Name] = value
		return CasesFromStmts(stmts[1:], path, nextSubst, work, depth+1, substitute, sideEffectFree)
	}
	if len(stmts) > 1 {
		first, ok := stmts[0].(*ast.IfStmt)
		if !ok || first == nil || len(first.Else) != 0 {
			return nil, false
		}
		cond, ok := substitute(first.Cond, subst, work)
		if !ok {
			return nil, false
		}
		thenCond := CombinePathCond(path, cond)
		thenCases, ok := CasesFromStmts(first.Then, thenCond, subst, work, depth+1, substitute, sideEffectFree)
		if !ok {
			return nil, false
		}
		negatedPrior := ast.Expr(&ast.UnaryExpr{Position: cond.Pos(), Op: lexer.TOKEN_NOT, Operand: cond})
		var out []Case
		out = append(out, thenCases...)
		for _, elif := range first.Elifs {
			if *work <= 0 {
				return nil, false
			}
			(*work)--
			elifCond, ok := substitute(elif.Cond, subst, work)
			if !ok {
				return nil, false
			}
			branchCond := CombinePathCond(path, &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: elifCond})
			cases, ok := CasesFromStmts(elif.Body, branchCond, subst, work, depth+1, substitute, sideEffectFree)
			if !ok {
				return nil, false
			}
			out = append(out, cases...)
			negatedPrior = &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: &ast.UnaryExpr{Position: elifCond.Pos(), Op: lexer.TOKEN_NOT, Operand: elifCond}}
		}
		fallthroughCases, ok := CasesFromStmts(stmts[1:], CombinePathCond(path, negatedPrior), subst, work, depth+1, substitute, sideEffectFree)
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
		value, ok := substitute(n.Value, subst, work)
		if !ok {
			return nil, false
		}
		return []Case{{Cond: path, Expr: value}}, true
	case *ast.IfStmt:
		if n == nil || n.Cond == nil {
			return nil, false
		}
		cond, ok := substitute(n.Cond, subst, work)
		if !ok {
			return nil, false
		}
		var out []Case
		thenCond := CombinePathCond(path, cond)
		thenCases, ok := CasesFromStmts(n.Then, thenCond, subst, work, depth+1, substitute, sideEffectFree)
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
			elifCond, ok := substitute(elif.Cond, subst, work)
			if !ok {
				return nil, false
			}
			branchCond := CombinePathCond(path, &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: elifCond})
			cases, ok := CasesFromStmts(elif.Body, branchCond, subst, work, depth+1, substitute, sideEffectFree)
			if !ok {
				return nil, false
			}
			out = append(out, cases...)
			negatedPrior = &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: &ast.UnaryExpr{Position: elifCond.Pos(), Op: lexer.TOKEN_NOT, Operand: elifCond}}
		}
		elseCond := CombinePathCond(path, negatedPrior)
		elseCases, ok := CasesFromStmts(n.Else, elseCond, subst, work, depth+1, substitute, sideEffectFree)
		if !ok {
			return nil, false
		}
		out = append(out, elseCases...)
		return out, true
	default:
		return nil, false
	}
}

func CombinePathCond(path, cond ast.Expr) ast.Expr {
	if path == nil {
		return cond
	}
	return &ast.BinaryExpr{Position: cond.Pos(), Op: lexer.TOKEN_AND, Left: path, Right: cond}
}

func CasesToExpr(cases []Case) ast.Expr {
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
