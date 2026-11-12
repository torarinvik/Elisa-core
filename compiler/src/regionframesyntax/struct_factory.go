package regionframesyntax

import "elisacore/src/ast"

func BuildsAndReturnsLocalStruct(fn *ast.FuncDecl, unwrap func(ast.Expr) ast.Expr, isAuto func(*ast.RegionStmt) bool, fresh func([]ast.Stmt, map[string]bool) bool) bool {
	hasAuto := false
	constructed := map[string]bool{}
	var collect func(stmts []ast.Stmt, inAuto bool)
	collect = func(stmts []ast.Stmt, inAuto bool) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.VarDeclStmt:
				if !inAuto {
					continue
				}
				switch v := unwrap(s.Value).(type) {
				case *ast.StructLitExpr:
					constructed[s.Name] = true
				case *ast.Ident:
					if constructed[v.Name] {
						constructed[s.Name] = true
					}
				}
			case *ast.IfStmt:
				collect(s.Then, inAuto)
				for _, elif := range s.Elifs {
					collect(elif.Body, inAuto)
				}
				collect(s.Else, inAuto)
			case *ast.WhileStmt:
				collect(s.Body, inAuto)
			case *ast.ForStmt:
				collect(s.Body, inAuto)
			case *ast.IterForStmt:
				collect(s.Body, inAuto)
			case *ast.ScopeStmt:
				collect(s.Body, inAuto)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					collect(arm.Body, inAuto)
				}
			case *ast.CanStmt:
				collect(s.Body, inAuto)
			case *ast.RegionStmt:
				// Only a synthesized auto region threads+adopts; an explicit `region NAME:` does not.
				if isAuto(s) {
					hasAuto = true
					collect(s.Body, true)
				}
			}
		}
	}
	collect(fn.Body, false)
	if !hasAuto {
		// No synthesized region: the body neither grows nor forwards the literal (either would
		// have opened one), so its container fields can only hold what the literal and direct
		// field assignments put there. When every region-carrying field is left at its fresh
		// default, the result owns no storage from any other region, and classifying the factory
		// region-polymorphic lets its call sites record the caller's ambient region as the
		// result's home instead of rejecting the local with "cannot infer region parameter"
		// when it is forwarded to a growing callee.
		collect(fn.Body, true)
		if !fresh(fn.Body, constructed) {
			return false
		}
	}
	if len(constructed) == 0 {
		return false
	}
	found := false
	var walk func(stmts []ast.Stmt)
	walk = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			if found {
				return
			}
			switch s := stmt.(type) {
			case *ast.ReturnStmt:
				if id, ok := unwrap(s.Value).(*ast.Ident); ok && constructed[id.Name] {
					found = true
				}
			case *ast.IfStmt:
				walk(s.Then)
				for _, elif := range s.Elifs {
					walk(elif.Body)
				}
				walk(s.Else)
			case *ast.WhileStmt:
				walk(s.Body)
			case *ast.ForStmt:
				walk(s.Body)
			case *ast.IterForStmt:
				walk(s.Body)
			case *ast.ScopeStmt:
				walk(s.Body)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					walk(arm.Body)
				}
			case *ast.CanStmt:
				walk(s.Body)
			case *ast.RegionStmt:
				if isAuto(s) {
					walk(s.Body)
				}
			}
		}
	}
	walk(fn.Body)
	return found
}
