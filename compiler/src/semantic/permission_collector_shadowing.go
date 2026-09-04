package semantic

import "elisacore/src/ast"

// The permission effect COLLECTOR runs after ordinary analysis, over a bare function body
// with no scope pushed: `a.currentScope` is the file scope, so every parameter and local is
// invisible to it. `globalStorageSymbolForIdent` therefore resolved a parameter named after
// a global TO that global, and a function as innocent as
//
//	global mutable hot: i32 = 0
//	def f(hot: i32) -> i32: return hot
//
// inferred Global.Read (and, under strict, Unsafe.MutableGlobal) from a name that never
// touches the global at all. The analyzer's own scope-aware paths get this right — f's
// fact_snapshot reports no required effect — so the collector was the only place that saw
// it, which is exactly why it went unnoticed until the self-hosted compiler grew the same
// family and disagreed.
//
// The fix is a shadow set built once per function: its parameters, plus every local
// declared anywhere in its body. Function-wide rather than scope-precise, and deliberately
// so — it is the identical rule stage1's collect_local_names applies, and matching rules is
// what keeps the two analyzers agreeing. Over-approximating here only ever SUPPRESSES an
// inferred effect, never invents one, so the imprecision cannot manufacture a diagnostic.
func collectShadowedNames(fn *ast.FuncDecl) map[string]bool {
	if fn == nil {
		return nil
	}
	shadowed := make(map[string]bool)
	for _, param := range fn.Params {
		if param.Name != "" {
			shadowed[param.Name] = true
		}
	}
	collectShadowedNamesInStmts(fn.Body, shadowed)
	if len(shadowed) == 0 {
		return nil
	}
	return shadowed
}

func collectShadowedNamesInStmts(stmts []ast.Stmt, shadowed map[string]bool) {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.VarDeclStmt:
			if n.Name != "" {
				shadowed[n.Name] = true
			}
		case *ast.IfStmt:
			collectShadowedNamesInStmts(n.Then, shadowed)
			for _, elif := range n.Elifs {
				collectShadowedNamesInStmts(elif.Body, shadowed)
			}
			collectShadowedNamesInStmts(n.Else, shadowed)
		case *ast.StaticIfStmt:
			collectShadowedNamesInStmts(n.Then, shadowed)
			collectShadowedNamesInStmts(n.Else, shadowed)
		case *ast.WhileStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.ForStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.IterForStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.ParallelForStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.MatchStmt:
			for _, arm := range n.Arms {
				collectShadowedNamesInStmts(arm.Body, shadowed)
			}
		case *ast.CanStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.ScopeStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.PoolStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.LockStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.RegionStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.InStoreStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.DeferStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.StaticBlockStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.CheckpointStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		case *ast.GroupedCheckpointStmt:
			collectShadowedNamesInStmts(n.Body, shadowed)
		}
	}
}

// globalStorageSymbolForIdent is the collector's shadow-aware view of the analyzer's
// lookup: a name the enclosing function binds is that binding, never the global.
func (c *permissionEffectCollector) globalStorageSymbolForIdent(name string) (*Symbol, bool) {
	if c.shadowed[name] {
		return nil, false
	}
	return c.analyzer.globalStorageSymbolForIdent(name)
}

// globalStorageRoot is the same shadow-aware view for a write target, whose root may sit
// under any number of `.field` / `[i]` / slice steps.
func (c *permissionEffectCollector) globalStorageRoot(expr ast.Expr) (*Symbol, bool) {
	if root, ok := globalStorageRootExpr(expr).(*ast.Ident); ok && c.shadowed[root.Name] {
		return nil, false
	}
	return c.analyzer.globalStorageRoot(expr)
}
