package semantic

import "elisacore/src/ast"

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
		fallback := cloneDefaultArgExpr(n.Fallback)
		if (n.Object != nil && object == nil) || (n.Index != nil && index == nil) || (n.Fallback != nil && fallback == nil) {
			return nil
		}
		return &ast.IndexExpr{Position: n.Position, Object: object, Index: index, Fallback: fallback, LegacyElseFallback: n.LegacyElseFallback}
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
		owner := cloneDefaultArgExpr(n.Owner)
		// Keys must be cloned too: a brace DICT literal carries its keys in Keys[i]
		// paired with value Elems[i]. Dropping Keys silently turns a `{k: v}` dict
		// default-arg into a `{v}` set literal (deep audit #15).
		keys := cloneDefaultArgExprs(n.Keys)
		if (len(n.Elems) != 0 && elems == nil) || (n.Owner != nil && owner == nil) || (len(n.Keys) != 0 && keys == nil) {
			return nil
		}
		return &ast.ListLitExpr{Position: n.Position, Elems: elems, Spreads: append([]bool(nil), n.Spreads...), Brace: n.Brace, Owner: owner, Keys: keys}
	case *ast.CallExpr:
		fn := cloneDefaultArgExpr(n.Func)
		safeReceiver := cloneDefaultArgExpr(n.SafeReceiver)
		args := cloneDefaultArgExprs(n.Args)
		if (n.Func != nil && fn == nil) || (n.SafeReceiver != nil && safeReceiver == nil) || (len(n.Args) != 0 && args == nil) {
			return nil
		}
		argItems := cloneDefaultCallArgItems(n.ArgItemOrder)
		if len(n.ArgItemOrder) != 0 && argItems == nil {
			return nil
		}
		return &ast.CallExpr{
			Position:      n.Position,
			Func:          fn,
			SafeReceiver:  safeReceiver,
			HasArgForward: n.HasArgForward,
			ArgForwardPos: n.ArgForwardPos,
			Args:          args,
			ArgNames:      append([]string(nil), n.ArgNames...),
			ArgShorthand:  append([]bool(nil), n.ArgShorthand...),
			ArgItemOrder:  argItems,
			Safe:          n.Safe,
		}
	case *ast.CastExpr:
		operand := cloneDefaultArgExpr(n.Operand)
		if n.Operand != nil && operand == nil {
			return nil
		}
		return &ast.CastExpr{Position: n.Position, Operand: operand, Target: n.Target, Origin: n.Origin}
	case *ast.LambdaExpr:
		bodyExpr := cloneDefaultArgExpr(n.BodyExpr)
		if n.BodyExpr != nil && bodyExpr == nil {
			return nil
		}
		body := cloneDefaultArgStmts(n.Body)
		if len(n.Body) != 0 && body == nil {
			return nil
		}
		params := append([]ast.ParamDecl(nil), n.Params...)
		for i := range params {
			if params[i].DefaultValue == nil {
				continue
			}
			params[i].DefaultValue = cloneDefaultArgExpr(params[i].DefaultValue)
			if params[i].DefaultValue == nil {
				return nil
			}
		}
		return &ast.LambdaExpr{
			Position:            n.Position,
			Keyword:             n.Keyword,
			UsesShorthandParams: n.UsesShorthandParams,
			Params:              params,
			ReturnType:          n.ReturnType,
			Body:                body,
			BodyExpr:            bodyExpr,
		}
	case *ast.SizeofExpr:
		return &ast.SizeofExpr{Position: n.Position, Type: n.Type}
	case *ast.AlignofExpr:
		return &ast.AlignofExpr{Position: n.Position, Type: n.Type}
	case *ast.OffsetofExpr:
		return &ast.OffsetofExpr{Position: n.Position, Type: n.Type, Field: n.Field}
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
		spreads := cloneDefaultArgExprs(n.Spreads)
		if len(n.Spreads) != 0 && spreads == nil {
			return nil
		}
		args := cloneDefaultArgExprs(n.Args)
		if len(n.Args) != 0 && args == nil {
			return nil
		}
		return &ast.StructLitExpr{
			Position: n.Position,
			Name:     n.Name,
			TypeArgs: append([]ast.TypeExpr(nil), n.TypeArgs...),
			Args:     args,
			ArgNames: append([]string(nil), n.ArgNames...),
			Brace:    n.Brace,
			Spreads:  spreads,
		}
	case *ast.RecordUpdateExpr:
		base := cloneDefaultArgExpr(n.Base)
		args := cloneDefaultArgExprs(n.Args)
		if (n.Base != nil && base == nil) || (len(n.Args) != 0 && args == nil) {
			return nil
		}
		return &ast.RecordUpdateExpr{
			Position: n.Position,
			Base:     base,
			Args:     args,
			ArgNames: append([]string(nil), n.ArgNames...),
		}
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
		return &ast.UnwrapElseExpr{Position: n.Position, Value: value, Fallback: fallback, Recovery: n.Recovery, LegacyImplicitElse: n.LegacyImplicitElse}
	case *ast.GetExpr:
		value := cloneDefaultArgExpr(n.Value)
		fallback := cloneDefaultArgExpr(n.Fallback)
		if (n.Value != nil && value == nil) || (n.Fallback != nil && fallback == nil) {
			return nil
		}
		return &ast.GetExpr{Position: n.Position, Value: value, Fallback: fallback, Recovery: n.Recovery}
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
		return &ast.AllocExpr{Position: n.Position, Owner: owner, Value: value, AutoRegion: n.AutoRegion}
	case *ast.CanExpr:
		exprClone := cloneDefaultArgExpr(n.Expr)
		if n.Expr != nil && exprClone == nil {
			return nil
		}
		return &ast.CanExpr{Position: n.Position, Expr: exprClone, Permissions: append([]ast.PermissionRef(nil), n.Permissions...), SuppressPermissionInference: n.SuppressPermissionInference}
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

func cloneDefaultArgStmts(stmts []ast.Stmt) []ast.Stmt {
	if len(stmts) == 0 {
		return nil
	}
	cloned := make([]ast.Stmt, 0, len(stmts))
	for _, stmt := range stmts {
		next := cloneDefaultArgStmt(stmt)
		if next == nil {
			return nil
		}
		cloned = append(cloned, next)
	}
	return cloned
}

func cloneDefaultArgStmt(stmt ast.Stmt) ast.Stmt {
	switch n := stmt.(type) {
	case *ast.ReturnStmt:
		value := cloneDefaultArgExpr(n.Value)
		if n.Value != nil && value == nil {
			return nil
		}
		return &ast.ReturnStmt{Position: n.Position, Value: value}
	case *ast.ExprStmt:
		expr := cloneDefaultArgExpr(n.Expr)
		if n.Expr != nil && expr == nil {
			return nil
		}
		return &ast.ExprStmt{Position: n.Position, Expr: expr}
	case *ast.AssertHoleStmt:
		return n
	case *ast.VarDeclStmt:
		value := cloneDefaultArgExpr(n.Value)
		if n.Value != nil && value == nil {
			return nil
		}
		owner := cloneDefaultArgExpr(n.Owner)
		if n.Owner != nil && owner == nil {
			return nil
		}
		return &ast.VarDeclStmt{Position: n.Position, Name: n.Name, Mutable: n.Mutable, Type: n.Type, Value: value, Owner: owner}
	default:
		return nil
	}
}

func cloneDefaultCallArgItems(items []ast.CallArgItem) []ast.CallArgItem {
	if len(items) == 0 {
		return nil
	}
	cloned := make([]ast.CallArgItem, 0, len(items))
	for _, item := range items {
		cloned = append(cloned, ast.CallArgItem{Position: item.Position, ArgIndex: item.ArgIndex})
	}
	return cloned
}

func (a *Analyzer) validateExpandedFuncParamDefaults(name string, specs []explicitParamSpec, paramTypes []Type) ([]ast.Expr, []bool) {
	if len(specs) == 0 {
		return nil, nil
	}
	defaultExprs := make([]ast.Expr, len(specs))
	hasDefaults := make([]bool, len(specs))
	sawDefault := false
	for i, spec := range specs {
		param := spec.Decl
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
		} else if spec.HasResolvedType && spec.ResolvedType != nil {
			expectedType = spec.ResolvedType
		}
		valid := false
		if spec.DefaultNamespace != "" || len(spec.DefaultUsings) != 0 {
			a.withResolutionContext(spec.DefaultNamespace, spec.DefaultUsings, func() {
				valid = a.validateParamDefaultExpr(param.DefaultValue, expectedType)
			})
		} else {
			valid = a.validateParamDefaultExpr(param.DefaultValue, expectedType)
		}
		if !valid {
			continue
		}
		defaultExprs[i] = param.DefaultValue
		hasDefaults[i] = true
	}
	return defaultExprs, hasDefaults
}

func (a *Analyzer) validateFuncParamDefaults(name string, params []ast.ParamDecl, paramTypes []Type) ([]ast.Expr, []bool) {
	if len(params) == 0 {
		return nil, nil
	}
	specs := make([]explicitParamSpec, 0, len(params))
	for _, param := range params {
		specs = append(specs, explicitParamSpec{Decl: param})
	}
	return a.validateExpandedFuncParamDefaults(name, specs, paramTypes)
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
		a.errorf(expr.Pos(), "default argument expects %s, got %s", expected, actualType)
		a.reportShapeMismatchNotes(expr.Pos(), expected, actualType)
		return false
	}
	return true
}
