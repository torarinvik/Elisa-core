package regionframesyntax

import (
	"elisacore/src/ast"
	"elisacore/src/astprunedscan"
)

func InferReturnViewRegion(fn *ast.FuncDecl, inferred map[string]string) {
	if len(inferred) != 1 {
		return
	}
	view, ok := fn.ReturnType.(*ast.BuiltinTypeExpr)
	if !ok || view.Name != "view" || view.Region != "" {
		return
	}
	for paramName, regionName := range inferred {
		if FunctionReturnsSliceOfParam(fn.Body, paramName) {
			view.Region = regionName
		}
	}
}

// FunctionReturnsSliceOfParam reports whether the body returns a slice whose object is the named
// parameter as a bare identifier, either directly (`return param[a:b]`) or through simple local
// aliases (`w = param[a:b]; return w`). Conservative: a false negative just leaves the return
// region-less (the pre-existing, safe-but-unchecked state), never the reverse.
func FunctionReturnsSliceOfParam(stmts []ast.Stmt, name string) bool {
	// SOUNDNESS: a local that is EVER aliased to the grown param's slice (on any path) is treated as
	// param-backed when returned, so the region stamp is applied and the escape checker can reject a
	// dangling return. We deliberately do NOT invalidate the alias on a later reassignment: doing so
	// flow-insensitively (a conditional `if c: w <- other`) would drop the stamp on the path that
	// still returns the param-backed view — a use-after-free hole (a regression that was caught at
	// runtime). Over-stamping only over-rejects (e.g. `w = param[a:b]; w <- other; return w`, where w
	// is always rebound), which is sound; precise flow-sensitive invalidation is a future refinement.
	aliases := map[string]bool{}
	valueIsParamSlice := func(expr ast.Expr) bool {
		if slice, ok := UnwrapRegionValue(expr).(*ast.SliceExpr); ok && slice != nil {
			if id, ok := UnwrapRegionValue(slice.Object).(*ast.Ident); ok && id != nil && id.Name == name {
				return true
			}
		}
		if id, ok := UnwrapRegionValue(expr).(*ast.Ident); ok && id != nil && aliases[id.Name] {
			return true
		}
		return false
	}
	var collect func([]ast.Stmt)
	collect = func(body []ast.Stmt) {
		for _, stmt := range body {
			switch s := stmt.(type) {
			case *ast.VarDeclStmt:
				if s.Value != nil && valueIsParamSlice(s.Value) {
					aliases[s.Name] = true
				}
			case *ast.AssignStmt:
				if id, ok := s.Target.(*ast.Ident); ok && id != nil && s.Value != nil && valueIsParamSlice(s.Value) {
					aliases[id.Name] = true
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
			case *ast.CanStmt:
				collect(s.Body)
			case *ast.RegionStmt:
				collect(s.Body)
			case *ast.InStoreStmt:
				collect(s.Body)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					collect(arm.Body)
				}
			}
		}
	}
	for {
		before := len(aliases)
		collect(stmts)
		if len(aliases) == before {
			break
		}
	}

	found := false
	astprunedscan.WalkPruned(stmts, func(node any) bool {
		if found {
			return false
		}
		if ret, ok := node.(*ast.ReturnStmt); ok && ret != nil {
			if valueIsParamSlice(ret.Value) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}
