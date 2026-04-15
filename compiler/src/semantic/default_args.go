package semantic

import "llcontext/src/ast"

func cloneDefaultArgExpr(expr ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case nil:
		return nil
	case *ast.Ident:
		return &ast.Ident{Position: n.Position, Name: n.Name}
	case *ast.IntLit:
		return &ast.IntLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix, IsHex: n.IsHex}
	case *ast.FloatLit:
		return &ast.FloatLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix}
	case *ast.StringLit:
		return &ast.StringLit{Position: n.Position, Value: n.Value}
	case *ast.CharLit:
		return &ast.CharLit{Position: n.Position, Value: n.Value}
	case *ast.BoolLit:
		return &ast.BoolLit{Position: n.Position, Value: n.Value}
	case *ast.NullLit:
		return &ast.NullLit{Position: n.Position}
	case *ast.ZeroedLit:
		return &ast.ZeroedLit{Position: n.Position}
	case *ast.ParenExpr:
		inner := cloneDefaultArgExpr(n.Inner)
		if n.Inner != nil && inner == nil {
			return nil
		}
		return &ast.ParenExpr{Position: n.Position, Inner: inner}
	case *ast.UnaryExpr:
		operand := cloneDefaultArgExpr(n.Operand)
		if n.Operand != nil && operand == nil {
			return nil
		}
		return &ast.UnaryExpr{Position: n.Position, Op: n.Op, Operand: operand}
	case *ast.BinaryExpr:
		left := cloneDefaultArgExpr(n.Left)
		right := cloneDefaultArgExpr(n.Right)
		if (n.Left != nil && left == nil) || (n.Right != nil && right == nil) {
			return nil
		}
		return &ast.BinaryExpr{Position: n.Position, Op: n.Op, Left: left, Right: right}
	case *ast.MoveExpr:
		operand := cloneDefaultArgExpr(n.Operand)
		if n.Operand != nil && operand == nil {
			return nil
		}
		return &ast.MoveExpr{Position: n.Position, Operand: operand}
	case *ast.FieldExpr:
		object := cloneDefaultArgExpr(n.Object)
		if n.Object != nil && object == nil {
			return nil
		}
		return &ast.FieldExpr{Position: n.Position, Object: object, Field: n.Field, Safe: n.Safe}
	case *ast.ShorthandMemberExpr:
		return &ast.ShorthandMemberExpr{Position: n.Position, Parts: append([]string(nil), n.Parts...)}
	case *ast.IndexExpr:
		object := cloneDefaultArgExpr(n.Object)
		index := cloneDefaultArgExpr(n.Index)
		if (n.Object != nil && object == nil) || (n.Index != nil && index == nil) {
			return nil
		}
		return &ast.IndexExpr{Position: n.Position, Object: object, Index: index}
	case *ast.SliceExpr:
		object := cloneDefaultArgExpr(n.Object)
		start := cloneDefaultArgExpr(n.Start)
		end := cloneDefaultArgExpr(n.End)
		if (n.Object != nil && object == nil) || (n.Start != nil && start == nil) || (n.End != nil && end == nil) {
			return nil
		}
		return &ast.SliceExpr{Position: n.Position, Object: object, Start: start, End: end}
	case *ast.ListLitExpr:
		elems := cloneDefaultArgExprs(n.Elems)
		if len(n.Elems) != 0 && elems == nil {
			return nil
		}
		return &ast.ListLitExpr{Position: n.Position, Elems: elems}
	case *ast.CallExpr:
		fn := cloneDefaultArgExpr(n.Func)
		args := cloneDefaultArgExprs(n.Args)
		if (n.Func != nil && fn == nil) || (len(n.Args) != 0 && args == nil) {
			return nil
		}
		withArgs := cloneDefaultWithArgs(n.WithArgs)
		if len(n.WithArgs) != 0 && withArgs == nil {
			return nil
		}
		withBundles := cloneDefaultWithBundles(n.WithBundles)
		if len(n.WithBundles) != 0 && withBundles == nil {
			return nil
		}
		withItems := cloneDefaultWithItems(n.WithItemOrder)
		if len(n.WithItemOrder) != 0 && withItems == nil {
			return nil
		}
		return &ast.CallExpr{
			Position:      n.Position,
			Func:          fn,
			HasArgForward: n.HasArgForward,
			ArgForwardPos: n.ArgForwardPos,
			Args:          args,
			ArgNames:      append([]string(nil), n.ArgNames...),
			Safe:          n.Safe,
			WithArgs:      withArgs,
			WithBundles:   withBundles,
			WithItemOrder: withItems,
		}
	case *ast.CastExpr:
		operand := cloneDefaultArgExpr(n.Operand)
		if n.Operand != nil && operand == nil {
			return nil
		}
		return &ast.CastExpr{Position: n.Position, Operand: operand, Target: n.Target, Origin: n.Origin, LegacySyntax: n.LegacySyntax}
	case *ast.SizeofExpr:
		return &ast.SizeofExpr{Position: n.Position, Type: n.Type}
	case *ast.TernaryExpr:
		value := cloneDefaultArgExpr(n.Value)
		cond := cloneDefaultArgExpr(n.Cond)
		alt := cloneDefaultArgExpr(n.Alt)
		if (n.Value != nil && value == nil) || (n.Cond != nil && cond == nil) || (n.Alt != nil && alt == nil) {
			return nil
		}
		return &ast.TernaryExpr{Position: n.Position, Value: value, Cond: cond, Alt: alt}
	case *ast.AddrOfExpr:
		operand := cloneDefaultArgExpr(n.Operand)
		if n.Operand != nil && operand == nil {
			return nil
		}
		return &ast.AddrOfExpr{Position: n.Position, Operand: operand}
	case *ast.SpecializeExpr:
		operand := cloneDefaultArgExpr(n.Operand)
		if n.Operand != nil && operand == nil {
			return nil
		}
		return &ast.SpecializeExpr{Position: n.Position, Operand: operand, TypeArgs: append([]ast.TypeExpr(nil), n.TypeArgs...)}
	case *ast.StructLitExpr:
		args := cloneDefaultArgExprs(n.Args)
		if len(n.Args) != 0 && args == nil {
			return nil
		}
		return &ast.StructLitExpr{Position: n.Position, Name: n.Name, TypeArgs: append([]ast.TypeExpr(nil), n.TypeArgs...), Args: args}
	case *ast.TupleExpr:
		elems := cloneDefaultArgExprs(n.Elems)
		if len(n.Elems) != 0 && elems == nil {
			return nil
		}
		return &ast.TupleExpr{Position: n.Position, Elems: elems}
	case *ast.TypeExprExpr:
		return &ast.TypeExprExpr{Position: n.Position, Type: n.Type}
	case *ast.RaiseExpr:
		errExpr := cloneDefaultArgExpr(n.Error)
		if n.Error != nil && errExpr == nil {
			return nil
		}
		return &ast.RaiseExpr{Position: n.Position, Error: errExpr}
	case *ast.TryExpr:
		value := cloneDefaultArgExpr(n.Value)
		fallback := cloneDefaultArgExpr(n.Fallback)
		if (n.Value != nil && value == nil) || (n.Fallback != nil && fallback == nil) {
			return nil
		}
		return &ast.TryExpr{Position: n.Position, Value: value, Fallback: fallback}
	case *ast.UnwrapElseExpr:
		value := cloneDefaultArgExpr(n.Value)
		fallback := cloneDefaultArgExpr(n.Fallback)
		if (n.Value != nil && value == nil) || (n.Fallback != nil && fallback == nil) {
			return nil
		}
		return &ast.UnwrapElseExpr{Position: n.Position, Value: value, Fallback: fallback}
	case *ast.OptionalBindExpr:
		value := cloneDefaultArgExpr(n.Value)
		if n.Value != nil && value == nil {
			return nil
		}
		return &ast.OptionalBindExpr{Position: n.Position, Name: n.Name, Value: value}
	case *ast.AllocExpr:
		owner := cloneDefaultArgExpr(n.Owner)
		value := cloneDefaultArgExpr(n.Value)
		if (n.Owner != nil && owner == nil) || (n.Value != nil && value == nil) {
			return nil
		}
		return &ast.AllocExpr{Position: n.Position, Owner: owner, Value: value}
	case *ast.CanExpr:
		exprClone := cloneDefaultArgExpr(n.Expr)
		if n.Expr != nil && exprClone == nil {
			return nil
		}
		return &ast.CanExpr{Position: n.Position, Expr: exprClone, Permissions: append([]ast.PermissionRef(nil), n.Permissions...)}
	default:
		return nil
	}
}

func cloneDefaultArgExprs(exprs []ast.Expr) []ast.Expr {
	if len(exprs) == 0 {
		return nil
	}
	cloned := make([]ast.Expr, 0, len(exprs))
	for _, expr := range exprs {
		next := cloneDefaultArgExpr(expr)
		if expr != nil && next == nil {
			return nil
		}
		cloned = append(cloned, next)
	}
	return cloned
}

func cloneDefaultWithArgs(args []ast.WithArg) []ast.WithArg {
	if len(args) == 0 {
		return nil
	}
	cloned := make([]ast.WithArg, 0, len(args))
	for _, arg := range args {
		value := cloneDefaultArgExpr(arg.Value)
		if arg.Value != nil && value == nil {
			return nil
		}
		cloned = append(cloned, ast.WithArg{Position: arg.Position, Name: arg.Name, Value: value, Shorthand: arg.Shorthand})
	}
	return cloned
}

func cloneDefaultWithBundles(bundles []ast.WithBundleUse) []ast.WithBundleUse {
	if len(bundles) == 0 {
		return nil
	}
	cloned := make([]ast.WithBundleUse, 0, len(bundles))
	for _, bundle := range bundles {
		args := cloneDefaultWithArgs(bundle.Args)
		if len(bundle.Args) != 0 && args == nil {
			return nil
		}
		cloned = append(cloned, ast.WithBundleUse{Position: bundle.Position, Name: bundle.Name, Args: args, Spread: bundle.Spread})
	}
	return cloned
}

func cloneDefaultWithItems(items []ast.WithItem) []ast.WithItem {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]ast.WithItem, 0, len(items))
	for _, item := range items {
		next := ast.WithItem{Position: item.Position, IsBundle: item.IsBundle}
		if item.IsBundle {
			bundles := cloneDefaultWithBundles([]ast.WithBundleUse{item.Bundle})
			if bundles == nil {
				return nil
			}
			next.Bundle = bundles[0]
		} else {
			args := cloneDefaultWithArgs([]ast.WithArg{item.Arg})
			if args == nil {
				return nil
			}
			next.Arg = args[0]
		}
		cloned = append(cloned, next)
	}
	return cloned
}

func (a *Analyzer) validateFuncParamDefaults(name string, params []ast.ParamDecl, paramTypes []Type) ([]ast.Expr, []bool) {
	if len(params) == 0 {
		return nil, nil
	}
	defaultExprs := make([]ast.Expr, len(params))
	hasDefaults := make([]bool, len(params))
	sawDefault := false
	for i, param := range params {
		if param.DefaultValue == nil {
			if sawDefault {
				a.errorf(param.Position, "parameter %q on function %q must declare a default because it follows a defaulted parameter", param.Name, name)
			}
			continue
		}
		sawDefault = true
		if cloneDefaultArgExpr(param.DefaultValue) == nil {
			a.errorf(param.DefaultValue.Pos(), "default value for parameter %q on function %q uses unsupported syntax in v1", param.Name, name)
			continue
		}
		var expectedType Type = invalidType
		if i < len(paramTypes) && paramTypes[i] != nil {
			expectedType = paramTypes[i]
		}
		if !a.validateParamDefaultExpr(param.DefaultValue, expectedType) {
			continue
		}
		defaultExprs[i] = param.DefaultValue
		hasDefaults[i] = true
	}
	return defaultExprs, hasDefaults
}

func (a *Analyzer) validateParamDefaultExpr(expr ast.Expr, expected Type) bool {
	if a == nil || expr == nil {
		return false
	}
	savedScope := a.currentScope
	savedReturn := a.currentReturn
	savedFuncDecl := a.currentFuncDecl
	savedFuncType := a.currentFuncType
	savedImplicitScopes := a.currentImplicitScopes
	a.currentScope = NewScope(a.globalScope)
	a.currentReturn = nil
	a.currentFuncDecl = nil
	a.currentFuncType = nil
	a.currentImplicitScopes = nil
	actualType := a.analyzeValueExpr(expr, expected)
	a.currentScope = savedScope
	a.currentReturn = savedReturn
	a.currentFuncDecl = savedFuncDecl
	a.currentFuncType = savedFuncType
	a.currentImplicitScopes = savedImplicitScopes
	if !AssignableTo(expected, actualType) {
		a.errorf(expr.Pos(), "default argument expects %s, got %s", expected.String(), actualType.String())
		a.reportShapeMismatchNotes(expr.Pos(), expected, actualType)
		return false
	}
	return true
}
