package semantic

import "llcontext/src/ast"

func resolveProjectedFieldCallArgByParamName(call *ast.CallExpr, decl *ast.ExternFuncDecl, paramName string) (ast.Expr, bool) {
	if call == nil || decl == nil || paramName == "" {
		return nil, false
	}
	for i, param := range decl.Params {
		if param.Name != paramName {
			continue
		}
		if i < len(call.Args) {
			return call.Args[i], true
		}
		return nil, false
	}
	return nil, false
}
func (a *Analyzer) immutableValueExprForSymbol(sym *Symbol) (ast.Expr, bool) {
	if sym == nil || sym.Mutable {
		return nil, false
	}
	declSym := sym
	if root := symbolAliasRoot(sym); root != nil {
		declSym = root
	}
	switch decl := declSym.Node.(type) {
	case *ast.VarDeclStmt:
		if decl == nil || decl.Value == nil {
			return nil, false
		}
		return decl.Value, true
	case *ast.ConstDecl:
		if decl == nil || decl.Value == nil {
			return nil, false
		}
		return decl.Value, true
	case *ast.TokenSetDecl:
		if decl == nil || decl.Value == nil {
			return nil, false
		}
		return decl.Value, true
	case *ast.GlobalDecl:
		if decl == nil || decl.Value == nil {
			return nil, false
		}
		return decl.Value, true
	default:
		return nil, false
	}
}
func (a *Analyzer) specializeProjectedFunctionFieldType(expr *ast.FieldExpr, declared Type) Type {
	if expr == nil || declared == nil {
		return declared
	}
	if _, ok := declared.(*FuncType); !ok {
		return declared
	}
	fieldExpr, ok := a.resolveProjectedFieldValueExpr(expr.Object, expr.Field)
	if !ok || fieldExpr == nil {
		return declared
	}
	actualType := a.analyzeExpr(fieldExpr)
	actualFunc, ok := actualType.(*FuncType)
	if !ok {
		return declared
	}
	if !actualFunc.ReturnProvenanceKnown {
		a.inferFuncReturnProvenanceForExpr(fieldExpr, actualFunc)
	}
	if !actualFunc.ReturnBorrowedOwnerRefsKnown {
		a.inferFuncReturnBorrowedOwnerRefsForExpr(fieldExpr, actualFunc)
	}
	if specialized, ok := a.specializeFunctionValueType(declared, actualFunc); ok {
		return specialized
	}
	return declared
}
