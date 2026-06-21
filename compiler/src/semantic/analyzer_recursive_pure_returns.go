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
	return pureReturnCasesFromStmts(decl.Body, nil)
}

func pureReturnCasesFromStmts(stmts []ast.Stmt, path ast.Expr) ([]pureReturnCase, bool) {
	if len(stmts) == 0 {
		return nil, false
	}
	if len(stmts) > 1 {
		first, ok := stmts[0].(*ast.IfStmt)
		if !ok || first == nil || len(first.Else) != 0 {
			return nil, false
		}
		thenCond := combinePathCond(path, first.Cond)
		thenCases, ok := pureReturnCasesFromStmts(first.Then, thenCond)
		if !ok {
			return nil, false
		}
		negatedPrior := ast.Expr(&ast.UnaryExpr{Position: first.Cond.Pos(), Op: lexer.TOKEN_NOT, Operand: first.Cond})
		var out []pureReturnCase
		out = append(out, thenCases...)
		for _, elif := range first.Elifs {
			cond := combinePathCond(path, &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: elif.Cond})
			cases, ok := pureReturnCasesFromStmts(elif.Body, cond)
			if !ok {
				return nil, false
			}
			out = append(out, cases...)
			negatedPrior = &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: &ast.UnaryExpr{Position: elif.Cond.Pos(), Op: lexer.TOKEN_NOT, Operand: elif.Cond}}
		}
		fallthroughCases, ok := pureReturnCasesFromStmts(stmts[1:], combinePathCond(path, negatedPrior))
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
		return []pureReturnCase{{Cond: path, Expr: n.Value}}, true
	case *ast.IfStmt:
		if n == nil || n.Cond == nil {
			return nil, false
		}
		var out []pureReturnCase
		thenCond := combinePathCond(path, n.Cond)
		thenCases, ok := pureReturnCasesFromStmts(n.Then, thenCond)
		if !ok {
			return nil, false
		}
		out = append(out, thenCases...)
		negatedPrior := ast.Expr(&ast.UnaryExpr{Position: n.Cond.Pos(), Op: lexer.TOKEN_NOT, Operand: n.Cond})
		for _, elif := range n.Elifs {
			if elif.Cond == nil {
				return nil, false
			}
			cond := combinePathCond(path, &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: elif.Cond})
			cases, ok := pureReturnCasesFromStmts(elif.Body, cond)
			if !ok {
				return nil, false
			}
			out = append(out, cases...)
			negatedPrior = &ast.BinaryExpr{Position: elif.Position, Op: lexer.TOKEN_AND, Left: negatedPrior, Right: &ast.UnaryExpr{Position: elif.Cond.Pos(), Op: lexer.TOKEN_NOT, Operand: elif.Cond}}
		}
		elseCond := combinePathCond(path, negatedPrior)
		elseCases, ok := pureReturnCasesFromStmts(n.Else, elseCond)
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
