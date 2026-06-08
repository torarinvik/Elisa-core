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
	granted := c.analyzer.grantedPermissionRefs(refs)
	savedSeen := c.seen
	c.seen = nil
	collect()
	bodyRefs := canonicalizePermissionRefs(c.seen)
	remainingRefs := missingGrantedPermissionRefs(bodyRefs, granted)
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
		if c.analyzer.enforceUnsafePermissions && c.analyzer.stmtRequiresUnsafeAlias(n) {
			c.addRefs(unsafeAliasRefs(n.Position))
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
		c.collectWriteTarget(n.Target, false)
		c.collectExpr(n.Value)
		if c.analyzer.enforceUnsafePermissions && c.analyzer.stmtRequiresUnsafeAlias(n) {
			c.addRefs(unsafeAliasRefs(n.Position))
		}
	case *ast.AugAssignStmt:
		c.collectWriteTarget(n.Target, true)
		c.collectExpr(n.Value)
	case *ast.AsRefAssignStmt:
		c.collectWriteTarget(n.Target, false)
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
		if n.As != "" {
			// Checked cast: discharge X within the body, surface Y instead.
			c.collectWithGrantedRefs(c.analyzer.resolvePermissionRefs(n.Permissions, false), func() {
				c.collectStmts(n.Body)
			})
			c.addRefs([]ast.PermissionRef{{Position: n.Position, Name: n.As}})
			break
		}
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
	case *ast.StaticAssertStmt:
		c.collectExpr(n.Cond)
		if n.Message != nil {
			c.collectExpr(n.Message)
		}
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			c.collectExpr(item.Cond)
			if item.Message != nil {
				c.collectExpr(item.Message)
			}
		}
	case *ast.StaticBlockStmt:
		c.collectStmts(n.Body)
	case *ast.DiscardStmt:
		c.collectExpr(n.Value)
	case *ast.RegionStmt:
		c.collectExpr(n.Capacity)
		c.collectStmts(n.Body)
	case *ast.LeakStmt:
		c.addRefs(unsafeLeakRefs(n.Position))
	}
}

func (c *permissionEffectCollector) collectExpr(expr ast.Expr) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if sym, ok := c.analyzer.globalStorageSymbolForIdent(n.Name); ok {
			c.addRefs(globalReadRefs(n.Position))
			if c.analyzer.enforceUnsafePermissions && sym.Kind == SymbolGlobal && sym.Mutable {
				c.addRefs(unsafeMutableGlobalRefs(n.Position))
			}
		}
		if c.analyzer.enforceUnsafePermissions {
			if c.analyzer.storageViewUseRequiresUnsafeStaleRef(n) {
				c.addRefs(unsafeStaleRefRefs(n.Position))
			}
		}
	case *ast.BinaryExpr:
		c.collectExpr(n.Left)
		c.collectExpr(n.Right)
		if c.analyzer.enforceUnsafePermissions && binaryExprRequiresUnsafePointerArithmetic(n.Op, c.analyzer.exprTypes[n.Left], c.analyzer.exprTypes[n.Right]) {
			c.addRefs(unsafePointerArithmeticRefs(n.Position))
		}
	case *ast.UnaryExpr:
		c.collectExpr(n.Operand)
	case *ast.CallExpr:
		c.collectExpr(n.Func)
		c.collectExpr(n.SafeReceiver)
		for _, arg := range n.Args {
			c.collectExpr(arg)
		}
		if c.analyzer.enforceUnsafePermissions && c.analyzer.exprRequiresUnsafeAlias(n) {
			c.addRefs(unsafeAliasRefs(n.Position))
		}
		if c.analyzer.enforceUnsafePermissions && isIndirectCallTarget(n.Func) {
			c.addRefs(unsafeIndirectCallRefs(n.Position))
		}
		if fnType, ok := c.analyzer.exprTypes[n.Func].(*FuncType); ok {
			c.addRefs(functionPermissionRefs(fnType))
			if c.analyzer.enforceUnsafePermissions {
				if fnType.Name == "spawn1" && len(n.Args) > 1 {
					if threadTransferRequiresUnsafeThreadShare(c.analyzer.exprTypes[n.Args[1]], map[string]bool{}) {
						c.addRefs(unsafeThreadShareRefs(n.Args[1].Pos()))
					}
				}
				if fnType.Name == "pool_submit1" && len(n.Args) > 2 {
					if threadTransferRequiresUnsafeThreadShare(c.analyzer.exprTypes[n.Args[2]], map[string]bool{}) {
						c.addRefs(unsafeThreadShareRefs(n.Args[2].Pos()))
					}
				}
				if resultPayload, ok := threadTransferResultPayloadType(fnType.Name, fnType.Return); ok && threadTransferRequiresUnsafeThreadShare(resultPayload, map[string]bool{}) {
					c.addRefs(unsafeThreadShareRefs(n.Position))
				}
			}
		}
	case *ast.FieldExpr:
		c.collectExpr(n.Object)
	case *ast.IndexExpr:
		c.collectExpr(n.Object)
		c.collectExpr(n.Index)
		c.collectExpr(n.Fallback)
		if c.analyzer.enforceUnsafePermissions && c.analyzer.indexExprRequiresUncheckedIndexPermission(n) {
			c.addRefs(unsafeUncheckedIndexRefs(n.Position))
		}
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
		if call, ok := c.analyzer.postfixShorthandCalls[n]; ok && call != nil {
			c.collectExpr(call)
			break
		}
		c.collectExpr(n.Operand)
		src := c.analyzer.exprTypes[n.Operand]
		dst := c.analyzer.exprTypes[n]
		if c.analyzer.enforceUnsafePermissions && n.Origin != ast.CastExprOriginIndirectCall && c.analyzer.castRequiresUnsafeGuestHostPointerCast(n, dst) {
			c.addRefs(unsafeGuestHostPointerCastRefs(n.Position))
		} else if c.analyzer.enforceUnsafePermissions && n.Origin != ast.CastExprOriginIndirectCall && (castRequiresUnsafePointerCast(src, dst) || c.analyzer.unsafeLifetimeWidenCasts[n]) {
			c.addRefs(unsafePointerCastRefs(n.Position))
		}
		if c.analyzer.enforceUnsafePermissions && c.analyzer.unsafeBufferReinterpretCasts[n] {
			c.addRefs(unsafeBufferReinterpretRefs(n.Position))
		}
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
		for _, spread := range n.Spreads {
			c.collectExpr(spread)
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
		for _, b := range n.Bindings {
			if vd, ok := b.(*ast.VarDeclStmt); ok {
				c.collectExpr(vd.Value)
			}
		}
		c.collectExpr(n.Key)
		c.collectExpr(n.Value)
		c.collectExpr(n.Source)
		c.collectExpr(n.RangeEnd)
		c.collectExpr(n.RangeStep)
		c.collectExpr(n.Filter)
		c.collectExpr(n.Owner)
	case *ast.QueryExpr:
		c.collectExpr(n.Source)
		c.collectExpr(n.Filter)
		c.collectExpr(n.Projection)
		c.collectExpr(n.Owner)
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
	case *ast.GetExpr:
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

func (c *permissionEffectCollector) collectWriteTarget(expr ast.Expr, alsoRead bool) {
	if expr == nil {
		return
	}
	if sym, ok := c.analyzer.globalStorageRoot(expr); ok {
		c.addRefs(globalWriteRefs(expr.Pos()))
		if alsoRead {
			c.addRefs(globalReadRefs(expr.Pos()))
		}
		if c.analyzer.enforceUnsafePermissions && sym.Kind == SymbolGlobal && sym.Mutable {
			c.addRefs(unsafeMutableGlobalRefs(expr.Pos()))
		}
	}
	switch n := expr.(type) {
	case *ast.FieldExpr:
		if globalStorageRootExpr(expr) != n.Object {
			c.collectWriteTarget(n.Object, alsoRead)
		}
	case *ast.IndexExpr:
		c.collectExpr(n.Index)
		c.collectExpr(n.Fallback)
	case *ast.SliceExpr:
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
	}
}
