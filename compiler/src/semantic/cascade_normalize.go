package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func normalizeCascadeStmts(file *ast.File) {
	if file == nil {
		return
	}
	normalizeCascadeDecls(file.Decls)
}

func normalizeCascadeDecls(decls []ast.Decl) {
	for _, decl := range decls {
		switch n := decl.(type) {
		case *ast.ConstDecl:
			n.Value = normalizeCascadeExpr(n.Value, nil, false)
		case *ast.ConstEnumDecl:
			for i := range n.Members {
				n.Members[i].Value = normalizeCascadeExpr(n.Members[i].Value, nil, false)
			}
		case *ast.GlobalDecl:
			n.Value = normalizeCascadeExpr(n.Value, nil, false)
		case *ast.StructDecl:
			for i := range n.DerivedStates {
				n.DerivedStates[i].Condition = normalizeCascadeExpr(n.DerivedStates[i].Condition, nil, false)
			}
		case *ast.NamespaceDecl:
			normalizeCascadeDecls(n.Decls)
		case *ast.StaticIfDecl:
			n.Cond = normalizeCascadeExpr(n.Cond, nil, false)
			normalizeCascadeDecls(n.Then)
			for i := range n.Elifs {
				n.Elifs[i].Cond = normalizeCascadeExpr(n.Elifs[i].Cond, nil, false)
				normalizeCascadeDecls(n.Elifs[i].Body)
			}
			normalizeCascadeDecls(n.Else)
		case *ast.ImplDecl:
			for _, member := range n.Members {
				if fn, ok := member.(*ast.FuncDecl); ok && fn != nil {
					normalizeCascadeParams(fn.Params)
					normalizeCascadeParams(fn.ImplicitParams)
					fn.Body = normalizeCascadeStmtList(fn.Body, nil)
				}
			}
		case *ast.FuncDecl:
			normalizeCascadeParams(n.Params)
			normalizeCascadeParams(n.ImplicitParams)
			n.Body = normalizeCascadeStmtList(n.Body, nil)
		case *ast.AttributeDecl:
			for i := range n.Arms {
				n.Arms[i].Guard = normalizeCascadeExpr(n.Arms[i].Guard, nil, false)
				n.Arms[i].Body = normalizeCascadeStmtList(n.Arms[i].Body, nil)
			}
		case *ast.ExternFuncDecl:
			normalizeCascadeParams(n.Params)
			normalizeCascadeParams(n.ImplicitParams)
		case *ast.ExportFuncDecl:
			normalizeCascadeParams(n.Params)
		}
	}
}

func normalizeCascadeParams(params []ast.ParamDecl) {
	for i := range params {
		params[i].DefaultValue = normalizeCascadeExpr(params[i].DefaultValue, nil, false)
	}
}

func normalizeCascadeStmtList(stmts []ast.Stmt, target ast.Expr) []ast.Stmt {
	if len(stmts) == 0 {
		return nil
	}
	normalized := make([]ast.Stmt, 0, len(stmts))
	for _, stmt := range stmts {
		normalized = append(normalized, normalizeCascadeStmt(stmt, target)...)
	}
	return normalized
}

func normalizeCascadeStmt(stmt ast.Stmt, target ast.Expr) []ast.Stmt {
	if stmt == nil {
		return nil
	}
	switch n := stmt.(type) {
	case *ast.CascadeStmt:
		n.Target = rewriteCascadeStmtHeadExpr(normalizeCascadeExpr(n.Target, target, true), target)
		return normalizeCascadeStmtList(n.Body, n.Target)
	case *ast.OpenStmt:
		n.Value = normalizeCascadeExpr(n.Value, target, false)
		n.Store = normalizeCascadeExpr(n.Store, target, false)
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.LocalParamsStmt:
		normalizeCascadeParams(n.Params)
	case *ast.ViewStmt:
		n.Value = normalizeCascadeExpr(n.Value, target, false)
		n.Store = normalizeCascadeExpr(n.Store, target, false)
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.DeferStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.IfStmt:
		n.Cond = normalizeCascadeExpr(n.Cond, target, false)
		n.Then = normalizeCascadeStmtList(n.Then, target)
		for i := range n.Elifs {
			n.Elifs[i].Cond = normalizeCascadeExpr(n.Elifs[i].Cond, target, false)
			n.Elifs[i].Body = normalizeCascadeStmtList(n.Elifs[i].Body, target)
		}
		n.Else = normalizeCascadeStmtList(n.Else, target)
	case *ast.WhileStmt:
		n.Cond = normalizeCascadeExpr(n.Cond, target, false)
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.ForStmt:
		n.Start = normalizeCascadeExpr(n.Start, target, false)
		n.End = normalizeCascadeExpr(n.End, target, false)
		n.Step = normalizeCascadeExpr(n.Step, target, false)
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.IterForStmt:
		n.Source = normalizeCascadeExpr(n.Source, target, false)
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.ParallelForStmt:
		n.Source = normalizeCascadeExpr(n.Source, target, false)
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.MatchStmt:
		n.Value = normalizeCascadeExpr(n.Value, target, false)
		n.Store = normalizeCascadeExpr(n.Store, target, false)
		for i := range n.Arms {
			n.Arms[i].Body = normalizeCascadeStmtList(n.Arms[i].Body, target)
		}
	case *ast.InStoreStmt:
		n.Store = normalizeCascadeExpr(n.Store, target, false)
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.CanStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.WithStmt:
		n.Args = normalizeCascadeWithArgs(n.Args, target, false)
		n.Bundles = normalizeCascadeWithBundles(n.Bundles, target, false)
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.ScopeStmt:
		n.Guard = normalizeCascadeExpr(n.Guard, target, false)
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.PoolStmt:
		n.Workers = normalizeCascadeExpr(n.Workers, target, false)
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.LockStmt:
		n.Mutex = normalizeCascadeExpr(n.Mutex, target, false)
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.StaticIfStmt:
		n.Cond = normalizeCascadeExpr(n.Cond, target, false)
		n.Then = normalizeCascadeStmtList(n.Then, target)
		for i := range n.Elifs {
			n.Elifs[i].Cond = normalizeCascadeExpr(n.Elifs[i].Cond, target, false)
			n.Elifs[i].Body = normalizeCascadeStmtList(n.Elifs[i].Body, target)
		}
		n.Else = normalizeCascadeStmtList(n.Else, target)
	case *ast.CheckpointStmt:
		n.Target = normalizeCascadeExpr(n.Target, target, false)
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.GroupedCheckpointStmt:
		for i := range n.Targets {
			n.Targets[i] = normalizeCascadeExpr(n.Targets[i], target, false)
		}
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.VarDeclStmt:
		n.Value = normalizeCascadeExpr(n.Value, target, false)
	case *ast.TupleBindStmt:
		n.Value = normalizeCascadeExpr(n.Value, target, false)
	case *ast.MoveBindStmt:
		n.Value = normalizeCascadeExpr(n.Value, target, false)
		n.Store = normalizeCascadeExpr(n.Store, target, false)
	case *ast.ReturnStmt:
		n.Value = normalizeCascadeExpr(n.Value, target, false)
	case *ast.PanicStmt:
		n.Message = normalizeCascadeExpr(n.Message, target, false)
	case *ast.ExprStmt:
		n.Expr = normalizeCascadeExpr(rewriteCascadeStmtHeadExpr(n.Expr, target), target, false)
	case *ast.AssignStmt:
		n.Target = rewriteCascadeStmtHeadExpr(n.Target, target)
		n.Value = normalizeCascadeExpr(n.Value, target, false)
	case *ast.AugAssignStmt:
		n.Target = rewriteCascadeStmtHeadExpr(n.Target, target)
		n.Value = normalizeCascadeExpr(n.Value, target, false)
	case *ast.AsRefAssignStmt:
		n.Target = rewriteCascadeStmtHeadExpr(n.Target, target)
		n.Value = normalizeCascadeExpr(n.Value, target, false)
	case *ast.DiscardStmt:
		n.Value = normalizeCascadeExpr(n.Value, target, false)
	case *ast.RegionStmt:
		n.Capacity = normalizeCascadeExpr(n.Capacity, target, false)
	}
	return []ast.Stmt{stmt}
}

func normalizeCascadeExpr(expr ast.Expr, target ast.Expr, rewriteShorthand bool) ast.Expr {
	switch n := expr.(type) {
	case nil:
		return nil
	case *ast.ShorthandMemberExpr:
		if rewriteShorthand && target != nil {
			return cascadeTargetFieldExpr(n.Position, target, n.Parts)
		}
		return n
	case *ast.ExprBlock:
		if rewriteShorthand {
			n.Stmts = normalizeCascadeStmtList(n.Stmts, target)
		} else {
			n.Stmts = normalizeCascadeStmtList(n.Stmts, nil)
		}
		n.Value = normalizeCascadeExpr(n.Value, target, rewriteShorthand)
		return n
	case *ast.BinaryExpr:
		n.Left = normalizeCascadeExpr(n.Left, target, rewriteShorthand)
		n.Right = normalizeCascadeExpr(n.Right, target, rewriteShorthand)
		return n
	case *ast.UnaryExpr:
		n.Operand = normalizeCascadeExpr(n.Operand, target, rewriteShorthand)
		return n
	case *ast.MoveExpr:
		n.Operand = normalizeCascadeExpr(n.Operand, target, rewriteShorthand)
		return n
	case *ast.CallExpr:
		n.Func = normalizeCascadeExpr(n.Func, target, rewriteShorthand)
		for i := range n.Args {
			n.Args[i] = normalizeCascadeExpr(n.Args[i], target, rewriteShorthand)
		}
		n.WithArgs = normalizeCascadeWithArgs(n.WithArgs, target, rewriteShorthand)
		n.WithBundles = normalizeCascadeWithBundles(n.WithBundles, target, rewriteShorthand)
		return n
	case *ast.FieldExpr:
		n.Object = normalizeCascadeExpr(n.Object, target, rewriteShorthand)
		return n
	case *ast.IndexExpr:
		n.Object = normalizeCascadeExpr(n.Object, target, rewriteShorthand)
		n.Index = normalizeCascadeExpr(n.Index, target, rewriteShorthand)
		n.Fallback = normalizeCascadeExpr(n.Fallback, target, rewriteShorthand)
		return n
	case *ast.SliceExpr:
		n.Object = normalizeCascadeExpr(n.Object, target, rewriteShorthand)
		n.Start = normalizeCascadeExpr(n.Start, target, rewriteShorthand)
		n.End = normalizeCascadeExpr(n.End, target, rewriteShorthand)
		return n
	case *ast.ListLitExpr:
		for i := range n.Elems {
			n.Elems[i] = normalizeCascadeExpr(n.Elems[i], target, rewriteShorthand)
		}
		return n
	case *ast.CastExpr:
		n.Operand = normalizeCascadeExpr(n.Operand, target, rewriteShorthand)
		return n
	case *ast.CascadeExpr:
		cascadeTarget := normalizeCascadeExpr(n.Target, target, true)
		return normalizeCascadeExpr(n.Value, cascadeTarget, true)
	case *ast.LambdaExpr:
		n.BodyExpr = normalizeCascadeExpr(n.BodyExpr, nil, false)
		n.Body = normalizeCascadeStmtList(n.Body, nil)
		return n
	case *ast.TernaryExpr:
		n.Value = normalizeCascadeExpr(n.Value, target, rewriteShorthand)
		n.Cond = normalizeCascadeExpr(n.Cond, target, rewriteShorthand)
		n.Alt = normalizeCascadeExpr(n.Alt, target, rewriteShorthand)
		return n
	case *ast.AddrOfExpr:
		n.Operand = normalizeCascadeExpr(n.Operand, target, rewriteShorthand)
		return n
	case *ast.SpecializeExpr:
		n.Operand = normalizeCascadeExpr(n.Operand, target, rewriteShorthand)
		return n
	case *ast.StructLitExpr:
		for i := range n.Args {
			n.Args[i] = normalizeCascadeExpr(n.Args[i], target, rewriteShorthand)
		}
		return n
	case *ast.TupleExpr:
		for i := range n.Elems {
			n.Elems[i] = normalizeCascadeExpr(n.Elems[i], target, rewriteShorthand)
		}
		return n
	case *ast.ParenExpr:
		n.Inner = normalizeCascadeExpr(n.Inner, target, rewriteShorthand)
		return n
	case *ast.RaiseExpr:
		n.Error = normalizeCascadeExpr(n.Error, target, rewriteShorthand)
		return n
	case *ast.TryExpr:
		n.Value = normalizeCascadeExpr(n.Value, target, rewriteShorthand)
		n.Fallback = normalizeCascadeExpr(n.Fallback, target, rewriteShorthand)
		return n
	case *ast.UnwrapElseExpr:
		n.Value = normalizeCascadeExpr(n.Value, target, rewriteShorthand)
		n.Fallback = normalizeCascadeExpr(n.Fallback, target, rewriteShorthand)
		return n
	case *ast.OptionalBindExpr:
		n.Value = normalizeCascadeExpr(n.Value, target, rewriteShorthand)
		return n
	case *ast.AllocExpr:
		n.Owner = normalizeCascadeExpr(n.Owner, target, rewriteShorthand)
		n.Value = normalizeCascadeExpr(n.Value, target, rewriteShorthand)
		n.NodeSpan = normalizeCascadeExpr(n.NodeSpan, target, rewriteShorthand)
		return n
	case *ast.CanExpr:
		n.Expr = normalizeCascadeExpr(n.Expr, target, rewriteShorthand)
		return n
	case *ast.MatchExpr:
		n.Value = normalizeCascadeExpr(n.Value, target, rewriteShorthand)
		n.Store = normalizeCascadeExpr(n.Store, target, rewriteShorthand)
		for i := range n.Arms {
			if rewriteShorthand {
				n.Arms[i].Body = normalizeCascadeStmtList(n.Arms[i].Body, target)
			} else {
				n.Arms[i].Body = normalizeCascadeStmtList(n.Arms[i].Body, nil)
			}
		}
		return n
	case *ast.VisitExpr:
		n.Value = normalizeCascadeExpr(n.Value, target, rewriteShorthand)
		n.Arms = normalizeCascadeVisitArms(n.Arms, target, rewriteShorthand)
		return n
	case *ast.FoldExpr:
		n.Value = normalizeCascadeExpr(n.Value, target, rewriteShorthand)
		n.Arms = normalizeCascadeVisitArms(n.Arms, target, rewriteShorthand)
		return n
	default:
		return expr
	}
}

func normalizeCascadeWithArgs(args []ast.WithArg, target ast.Expr, rewriteShorthand bool) []ast.WithArg {
	for i := range args {
		args[i].Value = normalizeCascadeExpr(args[i].Value, target, rewriteShorthand)
	}
	return args
}

func normalizeCascadeWithBundles(bundles []ast.WithBundleUse, target ast.Expr, rewriteShorthand bool) []ast.WithBundleUse {
	for i := range bundles {
		bundles[i].Args = normalizeCascadeWithArgs(bundles[i].Args, target, rewriteShorthand)
	}
	return bundles
}

func normalizeCascadeVisitArms(arms []ast.VisitArm, target ast.Expr, rewriteShorthand bool) []ast.VisitArm {
	for i := range arms {
		arms[i].Guard = normalizeCascadeExpr(arms[i].Guard, target, rewriteShorthand)
		if rewriteShorthand {
			arms[i].Body = normalizeCascadeStmtList(arms[i].Body, target)
		} else {
			arms[i].Body = normalizeCascadeStmtList(arms[i].Body, nil)
		}
	}
	return arms
}

func rewriteCascadeStmtHeadExpr(expr ast.Expr, target ast.Expr) ast.Expr {
	if expr == nil || target == nil {
		return expr
	}
	if rewritten, ok := rewriteCascadeHead(expr, target); ok {
		return rewritten
	}
	return expr
}

func rewriteCascadeHead(expr ast.Expr, target ast.Expr) (ast.Expr, bool) {
	switch n := expr.(type) {
	case *ast.ShorthandMemberExpr:
		return cascadeTargetFieldExpr(n.Position, target, n.Parts), true
	case *ast.ParenExpr:
		inner, ok := rewriteCascadeHead(n.Inner, target)
		if !ok {
			return expr, false
		}
		n.Inner = inner
		return n, true
	case *ast.FieldExpr:
		object, ok := rewriteCascadeHead(n.Object, target)
		if !ok {
			return expr, false
		}
		n.Object = object
		return n, true
	case *ast.CallExpr:
		fn, ok := rewriteCascadeHead(n.Func, target)
		if !ok {
			return expr, false
		}
		n.Func = fn
		return n, true
	case *ast.IndexExpr:
		object, ok := rewriteCascadeHead(n.Object, target)
		if !ok {
			return expr, false
		}
		n.Object = object
		return n, true
	case *ast.SliceExpr:
		object, ok := rewriteCascadeHead(n.Object, target)
		if !ok {
			return expr, false
		}
		n.Object = object
		return n, true
	case *ast.CastExpr:
		operand, ok := rewriteCascadeHead(n.Operand, target)
		if !ok {
			return expr, false
		}
		n.Operand = operand
		return n, true
	case *ast.SpecializeExpr:
		operand, ok := rewriteCascadeHead(n.Operand, target)
		if !ok {
			return expr, false
		}
		n.Operand = operand
		return n, true
	default:
		return expr, false
	}
}

func cascadeTargetFieldExpr(pos lexer.Pos, target ast.Expr, parts []string) ast.Expr {
	base := cloneDefaultArgExpr(target)
	if base == nil {
		base = target
	}
	expr := base
	for _, part := range parts {
		expr = &ast.FieldExpr{Position: pos, Object: expr, Field: part}
	}
	return expr
}
