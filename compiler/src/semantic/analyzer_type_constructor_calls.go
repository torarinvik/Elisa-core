package semantic

import "elisacore/src/ast"

func (a *Analyzer) analyzeTypeConstructorCall(expr *ast.CallExpr) (Type, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	targetExpr, ok := a.typeConstructorCallTarget(expr)
	if !ok {
		return nil, false
	}
	if len(expr.Args) != 1 || expr.NamedArgCount() != 0 || len(expr.ArgItemOrder) != 0 && expr.HasArgForward {
		a.errorf(expr.Pos(), "type constructor cast expects exactly 1 positional argument")
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType, true
	}
	targetType := a.resolveType(targetExpr)
	var sourceType Type
	if _, ok := expr.Args[0].(*ast.ZeroedLit); ok {
		sourceType = a.analyzeValueExpr(expr.Args[0], targetType)
	} else {
		sourceType = a.analyzeExpr(expr.Args[0])
	}
	if hookSym, ok := a.lookupVisibleCastHook(sourceType, targetType); ok && !a.isSelfCastHook(hookSym) {
		a.resolvedCastHooks[expr] = hookSym
		if fnType, ok := hookSym.Type.(*FuncType); ok {
			a.recordFunctionPermissionRefs(functionPermissionRefs(fnType))
		}
		return targetType, true
	}
	if !a.validCast(sourceType, targetType) {
		a.errorf(expr.Pos(), "invalid cast from %s to %s", sourceType, targetType)
	}
	return targetType, true
}

func (a *Analyzer) typeConstructorCallTarget(expr *ast.CallExpr) (ast.TypeExpr, bool) {
	if a == nil || expr == nil {
		return nil, false
	}
	targetExpr, ok := typeExprFromConstructorCallee(expr.Func)
	if !ok || !a.typeConstructorCalleeResolvesToType(targetExpr) {
		return nil, false
	}
	return targetExpr, true
}

func (a *Analyzer) isTypeConstructorCall(expr *ast.CallExpr) bool {
	_, ok := a.typeConstructorCallTarget(expr)
	return ok
}

func (a *Analyzer) typeConstructorCalleeResolvesToType(expr ast.TypeExpr) bool {
	if a == nil || expr == nil {
		return false
	}
	switch n := expr.(type) {
	case *ast.NamedType:
		if _, ok := a.lookupTypeParam(n.Name); ok {
			return true
		}
		if _, ok := a.namedTypes[n.Name]; ok {
			return true
		}
		_, _, ok := a.lookupVisibleType(n.Name)
		return ok
	case *ast.GenericType:
		if _, ok := a.lookupTypeParam(n.Name); ok {
			return true
		}
		if _, ok := a.namedTypes[n.Name]; ok {
			return true
		}
		_, _, ok := a.lookupVisibleType(n.Name)
		return ok
	case *ast.BuiltinTypeExpr:
		if _, ok := a.namedTypes[n.Name]; ok {
			return true
		}
		_, _, ok := a.lookupVisibleType(n.Name)
		return ok
	default:
		return false
	}
}

func typeExprFromConstructorCallee(expr ast.Expr) (ast.TypeExpr, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		if n == nil || n.Name == "" {
			return nil, false
		}
		return &ast.NamedType{Position: n.Position, Name: n.Name}, true
	case *ast.FieldExpr:
		name, ok := qualifiedTypePathFromExpr(n)
		if !ok || name == "" {
			return nil, false
		}
		return &ast.NamedType{Position: n.Position, Name: name}, true
	case *ast.TypeExprExpr:
		if n == nil || n.Type == nil {
			return nil, false
		}
		return n.Type, true
	case *ast.SpecializeExpr:
		name, ok := qualifiedTypePathFromExpr(n.Operand)
		if !ok || name == "" {
			return nil, false
		}
		return &ast.GenericType{Position: n.Position, Name: name, Args: n.TypeArgs}, true
	case *ast.ParenExpr:
		return typeExprFromConstructorCallee(n.Inner)
	default:
		return nil, false
	}
}
