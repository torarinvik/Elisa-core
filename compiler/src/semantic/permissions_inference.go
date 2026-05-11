package semantic

import (
	"elisacore/src/ast"
)

func (a *Analyzer) inferFunctionPermissionEffects(decls []scopedDecl) {
	for iter := 0; iter < len(decls)+4; iter++ {
		changed := false
		for _, scoped := range decls {
			fn, ok := scoped.Decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
				sym, ok := a.symbolForFuncDecl(fn)
				if !ok {
					return
				}
				fnType, ok := sym.Type.(*FuncType)
				if !ok || fnType == nil {
					return
				}
				usedRefs := a.collectFunctionPermissionRefs(fn)
				mergedRefs := mergePermissionRefs(fnType.DeclaredPermissionRefs, usedRefs)
				mergedFamilies := mergePermissionFamilies(fnType.DeclaredPermissions, permissionFamiliesFromRefs(mergedRefs))
				if !samePermissionRefs(fnType.PermissionRefs, mergedRefs) {
					fnType.PermissionRefs = mergedRefs
					changed = true
				}
				if !sameStringSlice(fnType.Permissions, mergedFamilies) {
					fnType.Permissions = mergedFamilies
					changed = true
				}
			})
		}
		if !changed {
			return
		}
	}
}

func (a *Analyzer) collectFunctionPermissionRefs(fn *ast.FuncDecl) []ast.PermissionRef {
	if fn == nil {
		return nil
	}
	collector := permissionEffectCollector{analyzer: a}
	collector.collectStmts(fn.Body)
	return collector.refs()
}

type permissionEffectCollector struct {
	analyzer *Analyzer
	seen     []ast.PermissionRef
}

func (c *permissionEffectCollector) refs() []ast.PermissionRef {
	return canonicalizePermissionRefs(c.seen)
}

func (c *permissionEffectCollector) addRefs(refs []ast.PermissionRef) {
	if len(refs) == 0 {
		return
	}
	c.seen = append(c.seen, refs...)
}

func (c *permissionEffectCollector) collectWithGrantedRefs(refs []ast.PermissionRef, collect func()) {
	refs = canonicalizePermissionRefs(refs)
	if len(refs) == 0 {
		collect()
		return
	}
	granted := grantedPermissionFamilies(permissionFamiliesFromRefs(refs))
	savedSeen := c.seen
	c.seen = nil
	collect()
	bodyRefs := canonicalizePermissionRefs(c.seen)
	remainingFamilies := missingGrantedPermissionFamilies(bodyRefs, granted)
	remainingRefs := filterPermissionRefsByFamilies(bodyRefs, remainingFamilies)
	c.seen = savedSeen
	c.addRefs(remainingRefs)
}

func (c *permissionEffectCollector) collectStmts(stmts []ast.Stmt) {
	for _, stmt := range stmts {
		c.collectStmt(stmt)
	}
}

func (c *permissionEffectCollector) collectStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		if n.Value != nil {
			c.collectExpr(n.Value)
		}
	case *ast.LetDestructureStmt:
		c.collectExpr(n.Value)
	case *ast.TupleBindStmt:
		c.collectExpr(n.Value)
	case *ast.MoveBindStmt:
		c.collectExpr(n.Value)
		if n.Store != nil {
			c.collectExpr(n.Store)
		}
	case *ast.ArgsScopeStmt:
		for _, arg := range n.Args {
			c.collectExpr(arg.Value)
		}
		for _, pack := range n.ParamPacks {
			for _, arg := range pack.Args {
				c.collectExpr(arg.Value)
			}
		}
		c.collectStmts(n.Body)
	case *ast.DeferStmt:
		c.collectStmts(n.Body)
	case *ast.AssignStmt:
		c.collectExpr(n.Target)
		c.collectExpr(n.Value)
	case *ast.AugAssignStmt:
		c.collectExpr(n.Target)
		c.collectExpr(n.Value)
	case *ast.AsRefAssignStmt:
		c.collectExpr(n.Target)
		c.collectExpr(n.Value)
	case *ast.ReturnStmt:
		c.collectExpr(n.Value)
	case *ast.IfStmt:
		c.collectExpr(n.Cond)
		c.collectStmts(n.Then)
		for _, elif := range n.Elifs {
			c.collectExpr(elif.Cond)
			c.collectStmts(elif.Body)
		}
		c.collectStmts(n.Else)
	case *ast.MatchStmt:
		c.collectExpr(n.Value)
		c.collectExpr(n.Store)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.InStoreStmt:
		c.collectExpr(n.Store)
		c.collectStmts(n.Body)
	case *ast.CanStmt:
		if n.SuppressPermissionInference {
			c.collectWithGrantedRefs(c.analyzer.resolvePermissionRefs(n.Permissions, false), func() {
				c.collectStmts(n.Body)
			})
			break
		}
		c.addRefs(c.analyzer.resolvePermissionRefs(n.Permissions, false))
		c.collectStmts(n.Body)
	case *ast.SignalStmt:
		c.addRefs(c.analyzer.resolvePermissionRefs(n.Permissions, false))
	case *ast.PoolStmt:
		c.collectExpr(n.Workers)
		c.addRefs([]ast.PermissionRef{{Position: n.Position, Name: "Pool", Member: "Create"}, {Position: n.Position, Name: "Pool", Member: "Shutdown"}})
		c.collectStmts(n.Body)
	case *ast.LockStmt:
		c.collectExpr(n.Mutex)
		c.addRefs([]ast.PermissionRef{{Position: n.Position, Name: "Sync", Member: "Lock"}, {Position: n.Position, Name: "Sync", Member: "Unlock"}})
		c.collectStmts(n.Body)
	case *ast.WhileStmt:
		c.collectExpr(n.Cond)
		c.collectStmts(n.Body)
	case *ast.ForStmt:
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
		if n.Step != nil {
			c.collectExpr(n.Step)
		}
		c.collectStmts(n.Body)
	case *ast.IterForStmt:
		c.collectExpr(n.Source)
		c.collectExpr(n.WhereFilter)
		c.collectExpr(n.Filter)
		c.collectStmts(n.Body)
	case *ast.ParallelForStmt:
		c.collectExpr(n.Source)
		c.addRefs([]ast.PermissionRef{
			{Position: n.Position, Name: "Pool", Member: "Submit"},
			{Position: n.Position, Name: "Pool", Member: "WaitAll"},
			{Position: n.Position, Name: "Memory", Member: "Allocate"},
			{Position: n.Position, Name: "Memory", Member: "Release"},
			{Position: n.Position, Name: "Abort", Member: "Panic"},
			{Position: n.Position, Name: "Atomics", Member: "Load"},
			{Position: n.Position, Name: "Atomics", Member: "CompareExchange"},
		})
		c.collectStmts(n.Body)
	case *ast.PanicStmt:
		c.addRefs([]ast.PermissionRef{{Position: n.Position, Name: "Abort", Member: "Panic"}})
		c.collectExpr(n.Message)
	case *ast.ExprStmt:
		c.collectExpr(n.Expr)
	case *ast.StaticIfStmt:
		for _, active := range c.analyzer.activeStmtBranch(n) {
			c.collectStmt(active)
		}
	case *ast.StaticErrorStmt:
		c.collectExpr(n.Message)
	case *ast.DiscardStmt:
		c.collectExpr(n.Value)
	case *ast.RegionStmt:
		c.collectExpr(n.Capacity)
		c.collectStmts(n.Body)
	}
}

func (c *permissionEffectCollector) collectExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		c.collectExpr(n.Left)
		c.collectExpr(n.Right)
	case *ast.UnaryExpr:
		c.collectExpr(n.Operand)
	case *ast.CallExpr:
		c.collectExpr(n.Func)
		c.collectExpr(n.SafeReceiver)
		for _, arg := range n.Args {
			c.collectExpr(arg)
		}
		if fnType, ok := c.analyzer.exprTypes[n.Func].(*FuncType); ok {
			c.addRefs(functionPermissionRefs(fnType))
		}
	case *ast.FieldExpr:
		c.collectExpr(n.Object)
	case *ast.IndexExpr:
		c.collectExpr(n.Object)
		c.collectExpr(n.Index)
		c.collectExpr(n.Fallback)
	case *ast.SliceExpr:
		c.collectExpr(n.Object)
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			c.collectExpr(elem)
		}
		c.collectExpr(n.Owner)
	case *ast.CastExpr:
		c.collectExpr(n.Operand)
		if sym, ok := c.analyzer.resolvedCastHooks[n]; ok {
			if fnType, ok := sym.Type.(*FuncType); ok {
				c.addRefs(functionPermissionRefs(fnType))
			}
		}
	case *ast.TernaryExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Cond)
		c.collectExpr(n.Alt)
	case *ast.AddrOfExpr:
		c.collectExpr(n.Operand)
	case *ast.MoveExpr:
		c.collectExpr(n.Operand)
	case *ast.SpecializeExpr:
		c.collectExpr(n.Operand)
	case *ast.StructLitExpr:
		if call, ok := c.analyzer.loweredInitCalls[n]; ok && call != nil {
			c.collectExpr(call)
			break
		}
		for _, arg := range n.Args {
			c.collectExpr(arg)
		}
	case *ast.RecordUpdateExpr:
		c.collectExpr(n.Base)
		for _, arg := range n.Args {
			c.collectExpr(arg)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			c.collectExpr(elem)
		}
	case *ast.ListComprehensionExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Source)
		c.collectExpr(n.RangeEnd)
		c.collectExpr(n.RangeStep)
		c.collectExpr(n.Filter)
	case *ast.QueryExpr:
		c.collectExpr(n.Source)
		c.collectExpr(n.Filter)
		c.collectExpr(n.Projection)
	case *ast.ParenExpr:
		c.collectExpr(n.Inner)
	case *ast.RaiseExpr:
		c.collectExpr(n.Error)
	case *ast.TryExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
	case *ast.UnwrapElseExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
	case *ast.OptionalBindExpr:
		c.collectExpr(n.Value)
	case *ast.AllocExpr:
		c.collectExpr(n.Owner)
		c.collectExpr(n.NodeSpan)
		c.collectExpr(n.Value)
	case *ast.CanExpr:
		if n.SuppressPermissionInference {
			c.collectWithGrantedRefs(c.analyzer.resolvePermissionRefs(n.Permissions, false), func() {
				c.collectExpr(n.Expr)
			})
			break
		}
		c.addRefs(c.analyzer.resolvePermissionRefs(n.Permissions, false))
		c.collectExpr(n.Expr)
	case *ast.MatchExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Store)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.VisitExpr:
		c.collectExpr(n.Value)
		for _, arm := range n.Arms {
			c.collectExpr(arm.Guard)
			c.collectStmts(arm.Body)
		}
	case *ast.FoldExpr:
		c.collectExpr(n.Value)
		for _, arm := range n.Arms {
			c.collectExpr(arm.Guard)
			c.collectStmts(arm.Body)
		}
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			c.collectExpr(target)
		}
	case *ast.IsAliasExpr:
		c.collectExpr(n.Target)
	}
}
