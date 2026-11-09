package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
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

func (a *Analyzer) staticStmtsContainBaseCaseForParam(stmts []ast.Stmt, paramName string, allowZeroEquality bool) bool {
	for index, stmt := range stmts {
		if a.staticStmtContainsBaseCaseForParam(stmt, paramName, allowZeroEquality) {
			return true
		}
		if a.staticStmtContainsFallthroughBaseCaseForParam(stmt, stmts[index+1:], paramName, allowZeroEquality) {
			return true
		}
	}
	return false
}

func (a *Analyzer) staticStmtsContainWeakSignedZeroBaseCaseForParam(stmts []ast.Stmt, paramName string) bool {
	for index, stmt := range stmts {
		if a.staticStmtContainsWeakSignedZeroBaseCaseForParam(stmt, paramName) {
			return true
		}
		if a.staticStmtContainsFallthroughWeakSignedZeroBaseCaseForParam(stmt, stmts[index+1:], paramName) {
			return true
		}
	}
	return false
}

func (a *Analyzer) staticStmtContainsFallthroughBaseCaseForParam(stmt ast.Stmt, following []ast.Stmt, paramName string, allowZeroEquality bool) bool {
	switch n := stmt.(type) {
	case *ast.IfStmt:
		return a.staticIfContainsBaseCaseForParam(n.Cond, n.Then, appendStaticStmtLists(n.Else, following), paramName, allowZeroEquality)
	case *ast.StaticIfStmt:
		return a.staticIfContainsBaseCaseForParam(n.Cond, n.Then, appendStaticStmtLists(n.Else, following), paramName, allowZeroEquality)
	default:
		return false
	}
}

func (a *Analyzer) staticStmtContainsFallthroughWeakSignedZeroBaseCaseForParam(stmt ast.Stmt, following []ast.Stmt, paramName string) bool {
	switch n := stmt.(type) {
	case *ast.IfStmt:
		return a.staticIfContainsWeakSignedZeroBaseCaseForParam(n.Cond, n.Then, appendStaticStmtLists(n.Else, following), paramName)
	case *ast.StaticIfStmt:
		return a.staticIfContainsWeakSignedZeroBaseCaseForParam(n.Cond, n.Then, appendStaticStmtLists(n.Else, following), paramName)
	default:
		return false
	}
}

func appendStaticStmtLists(first []ast.Stmt, second []ast.Stmt) []ast.Stmt {
	if len(first) == 0 {
		return second
	}
	if len(second) == 0 {
		return first
	}
	combined := make([]ast.Stmt, 0, len(first)+len(second))
	combined = append(combined, first...)
	combined = append(combined, second...)
	return combined
}

func (a *Analyzer) staticStmtContainsBaseCaseForParam(stmt ast.Stmt, paramName string, allowZeroEquality bool) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt:
		return a.staticReturnExprContainsBaseCaseForParam(n.Value, paramName, allowZeroEquality)
	case *ast.IfStmt:
		return a.staticIfContainsBaseCaseForParam(n.Cond, n.Then, n.Else, paramName, allowZeroEquality) || a.staticStmtsContainBaseCaseForParam(n.Then, paramName, allowZeroEquality) || a.staticStmtsContainBaseCaseForParam(n.Else, paramName, allowZeroEquality)
	case *ast.StaticIfStmt:
		return a.staticIfContainsBaseCaseForParam(n.Cond, n.Then, n.Else, paramName, allowZeroEquality) || a.staticStmtsContainBaseCaseForParam(n.Then, paramName, allowZeroEquality) || a.staticStmtsContainBaseCaseForParam(n.Else, paramName, allowZeroEquality)
	case *ast.StaticBlockStmt:
		return a.staticStmtsContainBaseCaseForParam(n.Body, paramName, allowZeroEquality)
	default:
		return false
	}
}

func (a *Analyzer) staticStmtContainsWeakSignedZeroBaseCaseForParam(stmt ast.Stmt, paramName string) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt:
		return a.staticReturnExprContainsWeakSignedZeroBaseCaseForParam(n.Value, paramName)
	case *ast.IfStmt:
		return a.staticIfContainsWeakSignedZeroBaseCaseForParam(n.Cond, n.Then, n.Else, paramName) || a.staticStmtsContainWeakSignedZeroBaseCaseForParam(n.Then, paramName) || a.staticStmtsContainWeakSignedZeroBaseCaseForParam(n.Else, paramName)
	case *ast.StaticIfStmt:
		return a.staticIfContainsWeakSignedZeroBaseCaseForParam(n.Cond, n.Then, n.Else, paramName) || a.staticStmtsContainWeakSignedZeroBaseCaseForParam(n.Then, paramName) || a.staticStmtsContainWeakSignedZeroBaseCaseForParam(n.Else, paramName)
	case *ast.StaticBlockStmt:
		return a.staticStmtsContainWeakSignedZeroBaseCaseForParam(n.Body, paramName)
	default:
		return false
	}
}

func (a *Analyzer) staticReturnExprContainsBaseCaseForParam(expr ast.Expr, paramName string, allowZeroEquality bool) bool {
	ternary, ok := expr.(*ast.TernaryExpr)
	if !ok {
		return false
	}
	then := []ast.Stmt{&ast.ReturnStmt{Position: ternary.Value.Pos(), Value: ternary.Value}}
	alt := []ast.Stmt{&ast.ReturnStmt{Position: ternary.Alt.Pos(), Value: ternary.Alt}}
	return a.staticIfContainsBaseCaseForParam(ternary.Cond, then, alt, paramName, allowZeroEquality)
}

func (a *Analyzer) staticReturnExprContainsWeakSignedZeroBaseCaseForParam(expr ast.Expr, paramName string) bool {
	ternary, ok := expr.(*ast.TernaryExpr)
	if !ok {
		return false
	}
	then := []ast.Stmt{&ast.ReturnStmt{Position: ternary.Value.Pos(), Value: ternary.Value}}
	alt := []ast.Stmt{&ast.ReturnStmt{Position: ternary.Alt.Pos(), Value: ternary.Alt}}
	return a.staticIfContainsWeakSignedZeroBaseCaseForParam(ternary.Cond, then, alt, paramName)
}

func (a *Analyzer) staticIfContainsBaseCaseForParam(cond ast.Expr, then []ast.Stmt, elseStmts []ast.Stmt, paramName string, allowZeroEquality bool) bool {
	comparison, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	leftParam, leftOK := staticExprIdentName(comparison.Left)
	rightValue, rightOK := a.evalStaticProofIntegerExpr(comparison.Right)
	if leftOK && rightOK && leftParam == paramName {
		return staticComparisonBranchTerminates(comparison.Op, rightValue, then, elseStmts, allowZeroEquality)
	}
	rightParam, rightParamOK := staticExprIdentName(comparison.Right)
	leftValue, leftValueOK := a.evalStaticProofIntegerExpr(comparison.Left)
	if rightParamOK && leftValueOK && rightParam == paramName {
		return staticComparisonBranchTerminates(staticReverseComparisonOp(comparison.Op), leftValue, then, elseStmts, allowZeroEquality)
	}
	return false
}

func (a *Analyzer) staticIfContainsWeakSignedZeroBaseCaseForParam(cond ast.Expr, then []ast.Stmt, elseStmts []ast.Stmt, paramName string) bool {
	comparison, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	leftParam, leftOK := staticExprIdentName(comparison.Left)
	rightValue, rightOK := a.evalStaticProofIntegerExpr(comparison.Right)
	if leftOK && rightOK && leftParam == paramName {
		return staticWeakSignedZeroComparisonBranchTerminates(comparison.Op, rightValue, then, elseStmts)
	}
	rightParam, rightParamOK := staticExprIdentName(comparison.Right)
	leftValue, leftValueOK := a.evalStaticProofIntegerExpr(comparison.Left)
	if rightParamOK && leftValueOK && rightParam == paramName {
		return staticWeakSignedZeroComparisonBranchTerminates(staticReverseComparisonOp(comparison.Op), leftValue, then, elseStmts)
	}
	return false
}

func staticComparisonBranchTerminates(op lexer.TokenKind, value int64, then []ast.Stmt, elseStmts []ast.Stmt, allowZeroEquality bool) bool {
	if staticParamComparisonTerminatesOnThen(op, value, allowZeroEquality) && staticStmtBlockAlwaysTerminates(then) {
		return true
	}
	return staticParamComparisonTerminatesOnElse(op, value, allowZeroEquality) && staticStmtBlockAlwaysTerminates(elseStmts)
}

func staticWeakSignedZeroComparisonBranchTerminates(op lexer.TokenKind, value int64, then []ast.Stmt, elseStmts []ast.Stmt) bool {
	if value != 0 {
		return false
	}
	switch op {
	case lexer.TOKEN_EQEQ:
		return staticStmtBlockAlwaysTerminates(then)
	case lexer.TOKEN_BANGEQ:
		return staticStmtBlockAlwaysTerminates(elseStmts)
	default:
		return false
	}
}

func staticExprIdentName(expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

func staticParamComparisonTerminatesOnThen(op lexer.TokenKind, value int64, allowZeroEquality bool) bool {
	switch op {
	case lexer.TOKEN_EQEQ:
		return allowZeroEquality && value == 0
	case lexer.TOKEN_LTEQ:
		return value >= 0
	case lexer.TOKEN_LT:
		return value > 0
	default:
		return false
	}
}

func staticParamComparisonTerminatesOnElse(op lexer.TokenKind, value int64, allowZeroEquality bool) bool {
	switch op {
	case lexer.TOKEN_BANGEQ:
		return allowZeroEquality && value == 0
	case lexer.TOKEN_GT:
		return value >= 0
	case lexer.TOKEN_GTEQ:
		return value > 0
	default:
		return false
	}
}

func staticReverseComparisonOp(op lexer.TokenKind) lexer.TokenKind {
	switch op {
	case lexer.TOKEN_LT:
		return lexer.TOKEN_GT
	case lexer.TOKEN_GT:
		return lexer.TOKEN_LT
	case lexer.TOKEN_LTEQ:
		return lexer.TOKEN_GTEQ
	case lexer.TOKEN_GTEQ:
		return lexer.TOKEN_LTEQ
	default:
		return op
	}
}
