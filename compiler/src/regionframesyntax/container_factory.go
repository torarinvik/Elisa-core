package regionframesyntax

import "elisacore/src/ast"

func BuildsAndReturnsLocalContainer(fn *ast.FuncDecl, owned func(ast.TypeExpr) bool, hasArena func(*ast.FuncDecl) bool, regionless func(ast.TypeExpr) bool, unwrap func(ast.Expr) ast.Expr, isAuto func(*ast.RegionStmt) bool) bool {
	if fn == nil || !owned(fn.ReturnType) {
		return false
	}
	// A function that threads an allocator (`Arena&` param) manages its own allocation: the parser
	// does NOT wrap its body in a synthesized auto region (maybeWrapFunctionBodyInAutoRegion's
	// hasArenaParam guard), so its returned container is NOT in an adoptable `__auto_*` region.
	// Classifying it region-polymorphic would inject a spurious `__region_auto` param and break its
	// callers (which build into the explicit arena, not an inferred region). Exclude it — mirror the
	// parser gate exactly.
	if hasArena(fn) {
		return false
	}
	// Collect locals declared as a region-LESS container literal that live in the inferred region:
	// the bare body or a synthesized `__auto_*` region scope. Deliberately do NOT descend into an
	// explicit `region NAME:` (a non-auto RegionStmt) or `in <arena>:` (InStoreStmt) — a container
	// built there belongs to that explicit region, is not adopted, and returning it still (correctly)
	// escapes. Only the synthesized-auto-region container is threaded+adopted from the caller.
	literalLocals := map[string]bool{}
	var collect func(stmts []ast.Stmt)
	collect = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.VarDeclStmt:
				if _, ok := unwrap(s.Value).(*ast.ListLitExpr); ok && regionless(s.Type) {
					literalLocals[s.Name] = true
				}
			case *ast.IfStmt:
				collect(s.Then)
				for _, elif := range s.Elifs {
					collect(elif.Body)
				}
				collect(s.Else)
			case *ast.WhileStmt:
				collect(s.Body)
			case *ast.ForStmt:
				collect(s.Body)
			case *ast.IterForStmt:
				collect(s.Body)
			case *ast.ScopeStmt:
				collect(s.Body)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					collect(arm.Body)
				}
			case *ast.CanStmt:
				collect(s.Body)
			case *ast.RegionStmt:
				// Only a synthesized auto region threads+adopts; an explicit `region NAME:` does not.
				if isAuto(s) {
					collect(s.Body)
				}
			}
		}
	}
	collect(fn.Body)
	if len(literalLocals) == 0 {
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
				if id, ok := unwrap(s.Value).(*ast.Ident); ok && literalLocals[id.Name] {
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
				walk(s.Body)
			case *ast.InStoreStmt:
				walk(s.Body)
			}
		}
	}
	walk(fn.Body)
	return found
}
