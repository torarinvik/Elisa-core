package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/staticwalk"
)

func (a *Analyzer) validateStaticFunctionRecursion(fn *ast.FuncDecl) {
	a.walkStaticStmts(fn.Body, func(expr ast.Expr) bool {
		call, ok := expr.(*ast.CallExpr)
		if ok {
			a.validateStaticRecursiveCall(fn, call)
		}
		return false
	})
}

func (a *Analyzer) validateStaticRecursiveCall(fn *ast.FuncDecl, call *ast.CallExpr) {
	if fn == nil || call == nil {
		return
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok {
		return
	}
	if ident.Name != fn.Name {
		a.validateStaticIndirectRecursiveCall(fn, call, ident.Name)
		return
	}
	if paramName, ok := a.staticRecursiveCallDecreasingParam(fn, call); ok {
		if a.staticFunctionHasBaseCaseForParam(fn, paramName) {
			return
		}
		if !a.staticFunctionParamIsUnsigned(fn, paramName) && a.staticFunctionHasWeakSignedZeroBaseCaseForParam(fn, paramName) {
			a.errorf(call.Pos(), "recursive static call to %q uses `== 0` as the only visible base case for signed parameter %q; use a lower-bound check such as `%s <= 0`", fn.Name, paramName, paramName)
			return
		}
		a.errorf(call.Pos(), "recursive static call to %q must have a visible terminating base case for parameter %q", fn.Name, paramName)
		return
	}
	a.errorf(call.Pos(), "recursive static call to %q must decrease a parameter using parameter - positive_constant", fn.Name)
}

func (a *Analyzer) validateStaticIndirectRecursiveCall(fn *ast.FuncDecl, call *ast.CallExpr, calleeName string) {
	callee := a.staticFunctionDeclByName(calleeName)
	if callee == nil || callee == fn {
		return
	}
	if !a.staticFunctionEventuallyCalls(callee, fn.Name, map[*ast.FuncDecl]bool{}) {
		return
	}
	a.errorf(call.Pos(), "indirect recursive static call cycle involving %q and %q is not supported; use direct structurally decreasing recursion", fn.Name, callee.Name)
}

func (a *Analyzer) staticFunctionDeclByName(name string) *ast.FuncDecl {
	sym, _, ok := a.lookupVisibleGlobal(name)
	if !ok || sym == nil {
		return nil
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok || fnType == nil || !fnType.Static {
		return nil
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || decl == nil || !decl.Static {
		return nil
	}
	return decl
}

func (a *Analyzer) staticFunctionEventuallyCalls(fn *ast.FuncDecl, targetName string, seen map[*ast.FuncDecl]bool) bool {
	if fn == nil {
		return false
	}
	if seen[fn] {
		return false
	}
	seen[fn] = true
	return a.walkStaticStmts(fn.Body, func(expr ast.Expr) bool {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		ident, ok := call.Func.(*ast.Ident)
		if !ok {
			return false
		}
		if ident.Name == targetName {
			return true
		}
		callee := a.staticFunctionDeclByName(ident.Name)
		return callee != nil && a.staticFunctionEventuallyCalls(callee, targetName, seen)
	})
}

// These wrappers keep analyzer call sites independent of the syntax traversal.
func (a *Analyzer) walkStaticStmts(stmts []ast.Stmt, visitExpr func(ast.Expr) bool) bool {
	return staticwalk.Statements(stmts, visitExpr)
}
func (a *Analyzer) walkStaticStmt(stmt ast.Stmt, visitExpr func(ast.Expr) bool) bool {
	return staticwalk.Statement(stmt, visitExpr)
}
func (a *Analyzer) walkStaticExpr(expr ast.Expr, visitExpr func(ast.Expr) bool) bool {
	return staticwalk.Expression(expr, visitExpr)
}
