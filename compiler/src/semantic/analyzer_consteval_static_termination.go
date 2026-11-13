package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/staticwalk"
)

func (a *Analyzer) staticRecursiveCallDecreasingParam(fn *ast.FuncDecl, call *ast.CallExpr) (string, bool) {
	args := call.Args
	if call.ResolvedArgsValid {
		args = call.ResolvedArgs
	}
	if len(args) != len(fn.Params) {
		return "", false
	}
	for index, param := range fn.Params {
		if a.staticExprIsPositiveDecrementOfParam(args[index], param.Name) {
			return param.Name, true
		}
	}
	return "", false
}

func (a *Analyzer) staticExprIsPositiveDecrementOfParam(expr ast.Expr, paramName string) bool {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok || binary.Op != lexer.TOKEN_MINUS {
		return false
	}
	ident, ok := binary.Left.(*ast.Ident)
	if !ok || ident.Name != paramName {
		return false
	}
	value, ok := a.evalStaticProofIntegerExpr(binary.Right)
	return ok && value > 0
}

func (a *Analyzer) evalStaticProofIntegerExpr(expr ast.Expr) (int64, bool) {
	value, ok := a.evalConstExpr(expr)
	if !ok || value.Kind != ConstInt {
		return 0, false
	}
	return value.Int, true
}

func (a *Analyzer) staticFunctionHasBaseCaseForParam(fn *ast.FuncDecl, paramName string) bool {
	if fn == nil {
		return false
	}
	return a.staticStmtsContainBaseCaseForParam(fn.Body, paramName, a.staticFunctionParamIsUnsigned(fn, paramName))
}

func (a *Analyzer) staticFunctionHasWeakSignedZeroBaseCaseForParam(fn *ast.FuncDecl, paramName string) bool {
	if fn == nil {
		return false
	}
	return a.staticStmtsContainWeakSignedZeroBaseCaseForParam(fn.Body, paramName)
}

func (a *Analyzer) staticFunctionParamIsUnsigned(fn *ast.FuncDecl, paramName string) bool {
	paramIndex := -1
	for index, param := range fn.Params {
		if param.Name == paramName {
			paramIndex = index
			break
		}
	}
	if paramIndex < 0 {
		return false
	}
	sym, ok := a.symbolForFuncDecl(fn)
	if !ok || sym == nil {
		return false
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || paramIndex >= len(fnType.Params) {
		return false
	}
	signed, _, ok := BitIntInfo(fnType.Params[paramIndex])
	if ok {
		return !signed
	}
	builtin, ok := fnType.Params[paramIndex].(*BuiltinType)
	if !ok || builtin == nil {
		return false
	}
	switch builtin.Name {
	case "usize", "uintptr":
		return true
	default:
		return false
	}
}

func (a *Analyzer) staticStmtsContainBaseCaseForParam(stmts []ast.Stmt, name string, allowZeroEquality bool) bool {
	return (staticwalk.BaseCaseScanner{Eval: a.evalStaticProofIntegerExpr}).Contains(stmts, name, allowZeroEquality)
}
func (a *Analyzer) staticStmtsContainWeakSignedZeroBaseCaseForParam(stmts []ast.Stmt, name string) bool {
	return (staticwalk.BaseCaseScanner{Eval: a.evalStaticProofIntegerExpr}).ContainsWeakSignedZero(stmts, name)
}
