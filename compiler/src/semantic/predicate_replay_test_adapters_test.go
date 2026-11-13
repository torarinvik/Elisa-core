package semantic

import "elisacore/src/ast"

func predicateReturnExpressions(fn *ast.FuncDecl) ([]ast.Expr, bool) {
	return predicateReturnExpressionsWithNodeBudget(fn, nil, nil)
}

func primitiveI64ReturnExpressions(fn *ast.FuncDecl) ([]ast.Expr, bool) {
	return primitiveI64ReturnExpressionsWithNodeBudget(fn, nil, nil)
}
