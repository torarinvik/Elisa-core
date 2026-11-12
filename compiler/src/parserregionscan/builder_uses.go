package parserregionscan

import "elisacore/src/ast"

func InferredDArrayBuilder(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	switch v := value.(type) {
	case *ast.ListComprehensionExpr:
		return true
	case *ast.QueryExpr:
		return v.Kind == ast.QueryExprEach
	}
	return false
}

func BlockLaterUsesUntypedListLiteralAsDArray(stmts []ast.Stmt, name string) bool {
	if name == "" {
		return false
	}
	seenDecl := false
	for _, stmt := range stmts {
		if !seenDecl {
			decl, ok := stmt.(*ast.VarDeclStmt)
			if !ok || decl.Name != name {
				continue
			}
			seenDecl = decl.Type == nil && isListLiteralExpr(decl.Value)
			continue
		}
		if stmtUsesDArrayBuilderLocal(stmt, name) {
			return true
		}
		if decl, ok := stmt.(*ast.VarDeclStmt); ok && decl.Name == name {
			return false
		}
	}
	return false
}

func isListLiteralExpr(value ast.Expr) bool {
	for {
		paren, ok := value.(*ast.ParenExpr)
		if !ok {
			break
		}
		value = paren.Inner
	}
	_, ok := value.(*ast.ListLitExpr)
	return ok
}

func stmtUsesDArrayBuilderLocal(stmt ast.Stmt, name string) bool {
	found := false
	var visitExpr func(ast.Expr)
	visitExpr = func(expr ast.Expr) {
		if found || expr == nil {
			return
		}
		switch e := expr.(type) {
		case *ast.CallExpr:
			if field, ok := e.Func.(*ast.FieldExpr); ok && field != nil {
				if ident, ok := field.Object.(*ast.Ident); ok && ident.Name == name {
					switch field.Field {
					case "push", "extend", "reserve", "resize", "clear", "truncate", "as_sview", "as_cstr":
						found = true
						return
					}
				}
			}
			visitExpr(e.Func)
			for _, arg := range e.Args {
				visitExpr(arg)
			}
		case *ast.FieldExpr:
			if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == name {
				switch e.Field {
				case "count", "capacity", "items":
					found = true
					return
				}
			}
			visitExpr(e.Object)
		case *ast.IndexExpr:
			if ident, ok := e.Object.(*ast.Ident); ok && ident.Name == name {
				found = true
				return
			}
			visitExpr(e.Object)
			visitExpr(e.Index)
		case *ast.BinaryExpr:
			visitExpr(e.Left)
			visitExpr(e.Right)
		case *ast.UnaryExpr:
			visitExpr(e.Operand)
		case *ast.ParenExpr:
			visitExpr(e.Inner)
		}
	}
	var visitStmt func(ast.Stmt)
	visitStmts := func(list []ast.Stmt) {
		for _, st := range list {
			if found {
				return
			}
			visitStmt(st)
		}
	}
	// Descend into nested control-flow BODIES, not just their headers: the canonical builder
	// shape fills the local inside a loop (`out = []; while ...: out.push(x)`). Without this
	// the auto-region wrap would miss a builder whose only use is loop-nested, leaving the
	// empty literal with no ambient region. Mirrors the semantic-side use-scan recursion.
	visitStmt = func(st ast.Stmt) {
		if found || st == nil {
			return
		}
		switch s := st.(type) {
		case *ast.ExprStmt:
			visitExpr(s.Expr)
		case *ast.ReturnStmt:
			visitExpr(s.Value)
		case *ast.VarDeclStmt:
			visitExpr(s.Value)
		case *ast.AssignStmt:
			visitExpr(s.Target)
			visitExpr(s.Value)
		case *ast.AugAssignStmt:
			visitExpr(s.Target)
			visitExpr(s.Value)
		case *ast.IfStmt:
			visitExpr(s.Cond)
			visitStmts(s.Then)
			for _, elif := range s.Elifs {
				visitExpr(elif.Cond)
				visitStmts(elif.Body)
			}
			visitStmts(s.Else)
		case *ast.ForStmt:
			visitExpr(s.Start)
			visitExpr(s.End)
			visitExpr(s.Step)
			visitStmts(s.Body)
		case *ast.IterForStmt:
			visitExpr(s.Source)
			visitExpr(s.Filter)
			visitStmts(s.Body)
		case *ast.WhileStmt:
			visitExpr(s.Cond)
			visitStmts(s.Body)
		case *ast.ScopeStmt:
			visitStmts(s.Body)
		case *ast.CanStmt:
			visitStmts(s.Body)
		case *ast.MatchStmt:
			for _, arm := range s.Arms {
				visitStmts(arm.Body)
			}
		}
	}
	visitStmt(stmt)
	return found
}

// isRegionlessContainerType reports whether a declared type is a growable container
// builtin with no `@r` region annotation — the signal that it will allocate and needs
// an ambient region.
func IsRegionlessContainerType(typ ast.TypeExpr) bool {
	for {
		mt, ok := typ.(*ast.MutableType)
		if !ok {
			break
		}
		typ = mt.Elem
	}
	// `dstr` is an owned string (the u8 specialization of darray, docs/26) but parses
	// as a NamedType, not a BuiltinTypeExpr — so a region-less `out: dstr = []` builder
	// must be recognized here too, or it never gets its inferred region (the auto-wrap
	// is what lets a string builder `push` bytes without an explicit `in <arena>:`).
	if nt, ok := typ.(*ast.NamedType); ok {
		return nt.Name == "dstr"
	}
	bt, ok := typ.(*ast.BuiltinTypeExpr)
	if !ok {
		return false
	}
	switch bt.Name {
	case "darray", "dict", "set", "dstr":
		return bt.Region == ""
	}
	return false
}
