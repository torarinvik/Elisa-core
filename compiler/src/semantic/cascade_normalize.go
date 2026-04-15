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
		case *ast.NamespaceDecl:
			normalizeCascadeDecls(n.Decls)
		case *ast.StaticIfDecl:
			normalizeCascadeDecls(n.Then)
			for i := range n.Elifs {
				normalizeCascadeDecls(n.Elifs[i].Body)
			}
			normalizeCascadeDecls(n.Else)
		case *ast.ImplDecl:
			for _, member := range n.Members {
				if fn, ok := member.(*ast.FuncDecl); ok && fn != nil {
					fn.Body = normalizeCascadeStmtList(fn.Body, nil)
				}
			}
		case *ast.FuncDecl:
			n.Body = normalizeCascadeStmtList(n.Body, nil)
		}
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
		n.Target = rewriteCascadeStmtHeadExpr(n.Target, target)
		return normalizeCascadeStmtList(n.Body, n.Target)
	case *ast.OpenStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.ViewStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.DeferStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.IfStmt:
		n.Then = normalizeCascadeStmtList(n.Then, target)
		for i := range n.Elifs {
			n.Elifs[i].Body = normalizeCascadeStmtList(n.Elifs[i].Body, target)
		}
		n.Else = normalizeCascadeStmtList(n.Else, target)
	case *ast.WhileStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.ForStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.IterForStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.ParallelForStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.MatchStmt:
		for i := range n.Arms {
			n.Arms[i].Body = normalizeCascadeStmtList(n.Arms[i].Body, target)
		}
	case *ast.InStoreStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.CanStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.WithStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.ScopeStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.PoolStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.LockStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.StaticIfStmt:
		n.Then = normalizeCascadeStmtList(n.Then, target)
		for i := range n.Elifs {
			n.Elifs[i].Body = normalizeCascadeStmtList(n.Elifs[i].Body, target)
		}
		n.Else = normalizeCascadeStmtList(n.Else, target)
	case *ast.CheckpointStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.GroupedCheckpointStmt:
		n.Body = normalizeCascadeStmtList(n.Body, target)
	case *ast.ExprStmt:
		n.Expr = rewriteCascadeStmtHeadExpr(n.Expr, target)
	case *ast.AssignStmt:
		n.Target = rewriteCascadeStmtHeadExpr(n.Target, target)
	case *ast.AugAssignStmt:
		n.Target = rewriteCascadeStmtHeadExpr(n.Target, target)
	case *ast.AsRefAssignStmt:
		n.Target = rewriteCascadeStmtHeadExpr(n.Target, target)
	}
	return []ast.Stmt{stmt}
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
