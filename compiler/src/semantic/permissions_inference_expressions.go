package semantic

import "elisacore/src/ast"

func (c *permissionEffectCollector) collectExpr(expr ast.Expr) {
	c.collectExprWithValueContext(expr, true)
}

func (c *permissionEffectCollector) collectExprWithValueContext(expr ast.Expr, wholeValue bool) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.Ident:
		sym, ok := c.analyzer.globalStorageRoot(n)
		_, mutableGlobal := c.analyzer.mutableGlobalStorageRoot(n)
		wholeMutableGlobal := c.mutableGlobalsOnly && wholeValue && c.analyzer.globalWholeValueReadRequiresGrant(n)
		if ok && (!c.mutableGlobalsOnly || mutableGlobal || wholeMutableGlobal) {
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
		// An operator lowered to a protocol-impl call (`a + b` -> `T.__add__(a, b)`) carries the
		// callee's effects: collect the lowered call (which contains the operands as its args) INSTEAD
		// of the operands directly, so an allocating `__add__` requires `can Memory.Allocate` at the
		// operator site. Mirrors the postfix-shorthand-call handling.
		if n.LoweredCall != nil {
			c.collectExpr(n.LoweredCall)
			break
		}
		c.collectExpr(n.Left)
		c.collectExpr(n.Right)
		if c.analyzer.enforceUnsafePermissions && binaryExprRequiresUnsafePointerArithmetic(n.Op, c.analyzer.exprType(n.Left), c.analyzer.exprType(n.Right)) {
			c.addRefs(unsafePointerArithmeticRefs(n.Position))
		}
	case *ast.UnaryExpr:
		// An overloaded unary operator (`-x` -> `T.__neg__(x)`) carries the callee's effects.
		if n.LoweredCall != nil {
			c.collectExpr(n.LoweredCall)
			break
		}
		c.collectExpr(n.Operand)
	case *ast.CallExpr:
		c.addRefs(c.analyzer.mutableGlobalCallWriteRefs(n))
		c.addRefs(c.analyzer.mutableGlobalCallbackRefs(n))
		c.collectExpr(n.Func)
		c.collectExpr(n.SafeReceiver)
		for _, arg := range n.LoweredArgs() {
			c.collectExpr(arg)
		}
		if c.analyzer.enforceUnsafePermissions && c.analyzer.exprRequiresUnsafeAlias(n) {
			c.addRefs(unsafeAliasRefs(n.Position))
		}
		if c.analyzer.enforceUnsafePermissions && isIndirectCallTarget(n.Func) {
			c.addRefs(unsafeIndirectCallRefs(n.Position))
		}
		if fnType, ok := c.analyzer.exprType(n.Func).(*FuncType); ok {
			if c.mutableGlobalsOnly {
				c.addRefs(c.analyzer.mandatoryGlobalPermissionRefs(fnType))
			} else {
				c.addRefs(c.analyzer.effectiveFunctionPermissionRefs(fnType))
			}
			if c.analyzer.enforceUnsafePermissions {
				if (fnType.Name == "spawn1" || fnType.Name == "spawn_daemon") && len(n.Args) > 1 {
					if threadTransferRequiresUnsafeThreadShare(c.analyzer.exprType(n.Args[1]), map[string]bool{}) {
						c.addRefs(unsafeThreadShareRefs(n.Args[1].Pos()))
					}
				}
				if fnType.Name == "pool_submit1" && len(n.Args) > 2 {
					if threadTransferRequiresUnsafeThreadShare(c.analyzer.exprType(n.Args[2]), map[string]bool{}) {
						c.addRefs(unsafeThreadShareRefs(n.Args[2].Pos()))
					}
				}
				if resultPayload, ok := threadTransferResultPayloadType(fnType.Name, fnType.Return); ok && threadTransferRequiresUnsafeThreadShare(resultPayload, map[string]bool{}) {
					c.addRefs(unsafeThreadShareRefs(n.Position))
				}
			}
		}
	case *ast.FieldExpr:
		if c.mutableGlobalsOnly && (c.analyzer.globalProjectionBeginsMutablePath(n) || (wholeValue && c.analyzer.globalWholeValueReadRequiresGrant(n))) {
			c.addRefs(globalReadRefs(n.Position))
		}
		c.collectExprWithValueContext(n.Object, false)
	case *ast.IndexExpr:
		if c.mutableGlobalsOnly && (c.analyzer.globalProjectionBeginsMutablePath(n) || (wholeValue && c.analyzer.globalWholeValueReadRequiresGrant(n))) {
			c.addRefs(globalReadRefs(n.Position))
		}
		c.collectExprWithValueContext(n.Object, false)
		c.collectExpr(n.Index)
		c.collectExpr(n.Fallback)
		if c.analyzer.enforceUnsafePermissions && c.analyzer.indexExprRequiresUncheckedIndexPermission(n) {
			c.addRefs(unsafeUncheckedIndexRefs(n.Position))
		}
	case *ast.SliceExpr:
		if c.mutableGlobalsOnly && (c.analyzer.globalProjectionBeginsMutablePath(n) || (wholeValue && c.analyzer.globalWholeValueReadRequiresGrant(n))) {
			c.addRefs(globalReadRefs(n.Position))
		}
		c.collectExprWithValueContext(n.Object, false)
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
		c.collectExprWithValueContext(n.Operand, wholeValue)
		src := c.analyzer.exprType(n.Operand)
		dst := c.analyzer.exprType(n)
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
				if c.mutableGlobalsOnly {
					c.addRefs(c.analyzer.mandatoryGlobalPermissionRefs(fnType))
				} else {
					c.addRefs(c.analyzer.effectiveFunctionPermissionRefs(fnType))
				}
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
		c.addRefs(c.analyzer.mutableGlobalStoreRowsReadRefs(n.Source))
		for _, b := range n.Bindings {
			if vd, ok := b.(*ast.VarDeclStmt); ok {
				c.collectStmt(vd)
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
		c.addRefs(c.analyzer.mutableGlobalStoreRowsReadRefs(n.Source))
		c.collectExpr(n.Source)
		c.collectExpr(n.Filter)
		c.collectExpr(n.Projection)
		c.collectExpr(n.Owner)
	case *ast.LambdaExpr:
		if ft, ok := c.analyzer.exprType(n).(*FuncType); ok {
			collector := permissionEffectCollector{analyzer: c.analyzer, mutableGlobalsOnly: true, returnType: ft.Return}
			collector.collectStmts(n.Body)
			collector.collectExpr(n.BodyExpr)
			refs := collector.refs()
			changed := !samePermissionRefs(ft.MutableGlobalPermissionRefs, refs)
			ft.MutableGlobalPermissionRefs = refs
			if changed {
				a := c.analyzer
				if a.permissionInference != nil {
					a.permissionInference.changed(ft)
				}
			}
		}
	case *ast.ExprBlock:
		// A value block is an executable expression. Its leading statements and
		// tail value can contain calls/effects just like a function body; skipping
		// it under-reports effects when a block is used as an initializer or return.
		c.collectStmts(n.Stmts)
		c.collectExpr(n.Value)
	case *ast.ParenExpr:
		c.collectExprWithValueContext(n.Inner, wholeValue)
	case *ast.RaiseExpr:
		c.collectExpr(n.Error)
	case *ast.TryExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
		if n.Recovery != nil {
			c.collectExpr(n.Recovery.Value)
			c.collectStmts(n.Recovery.Body)
		}
	case *ast.UnwrapElseExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
		if n.Recovery != nil {
			c.collectExpr(n.Recovery.Value)
			c.collectStmts(n.Recovery.Body)
		}
	case *ast.GetExpr:
		c.collectExpr(n.Value)
		c.collectExpr(n.Fallback)
		if n.Recovery != nil {
			c.collectExpr(n.Recovery.Value)
			c.collectStmts(n.Recovery.Body)
		}
	case *ast.CatchExpr:
		c.collectExpr(n.Value)
		c.collectStmts(n.Success.Body)
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
		}
	case *ast.OptionalBindExpr:
		c.collectExpr(n.Value)
	case *ast.AllocExpr:
		c.collectExpr(n.Owner)
		c.collectExpr(n.Value)
	case *ast.CanExpr:
		if n.SuppressPermissionInference {
			c.collectWithGrantedRefs(trustedUnsafePermissionRefs(c.analyzer.resolvePermissionRefs(n.Permissions, false)), func() {
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
			// Match guards are executable expressions. Omitting them from permission
			// inference under-reports effects (and call-graph capabilities) whenever
			// a guard invokes an extern or another effectful function.
			c.collectExpr(arm.Guard)
			c.collectStmts(arm.Body)
		}
	case *ast.FoldExpr:
		c.addRefs(c.analyzer.mutableGlobalStoreRowsReadRefs(n.Value))
		c.addRefs(c.analyzer.mutableGlobalFoldEmitRefs(n))
		c.collectExpr(n.Value)
		for _, arm := range n.Arms {
			c.collectExpr(arm.Guard)
			c.collectStmts(arm.Body)
		}
	case *ast.EmitExpr:
		c.collectExpr(n.Value)
	case *ast.MachineFromExpr:
		if n.Lowered != nil {
			c.collectExpr(n.Lowered)
			break
		}
		c.addRefs(c.analyzer.mutableGlobalMachinePayloadRefs(n))
		for _, arg := range n.StartArgs {
			c.collectExpr(arg)
		}
		c.collectExpr(n.Decreases)
		for _, declaration := range n.HeaderDecls {
			c.collectStmt(declaration)
		}
		for _, arm := range n.Arms {
			c.collectStmts(arm.Body)
			for _, term := range arm.Terminators {
				c.collectExpr(term.Guard)
				for _, arg := range term.Args {
					c.collectExpr(arg)
				}
				c.collectExpr(term.Value)
			}
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
	_, mutableGlobal := c.analyzer.mutableGlobalStorageRoot(expr)
	if sym, ok := c.analyzer.globalStorageRoot(expr); ok && (!c.mutableGlobalsOnly || mutableGlobal) {
		c.addRefs(globalWriteRefs(expr.Pos()))
		if c.analyzer.globalPathLoadsMutableBorrow(expr) {
			c.addRefs(globalReadRefs(expr.Pos()))
		}
		if alsoRead {
			c.addRefs(globalReadRefs(expr.Pos()))
		}
		if c.analyzer.enforceUnsafePermissions && sym.Kind == SymbolGlobal && sym.Mutable {
			c.addRefs(unsafeMutableGlobalRefs(expr.Pos()))
		}
	}
	switch n := expr.(type) {
	case *ast.FieldExpr:
		c.collectTargetOperands(n.Object)
	case *ast.IndexExpr:
		c.collectTargetOperands(n.Object)
		c.collectExpr(n.Index)
		c.collectExpr(n.Fallback)
	case *ast.SliceExpr:
		c.collectTargetOperands(n.Object)
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
	case *ast.ParenExpr:
		c.collectTargetOperands(n.Inner)
	}
}

func (c *permissionEffectCollector) collectTargetOperands(expr ast.Expr) {
	switch n := expr.(type) {
	case *ast.FieldExpr:
		c.collectTargetOperands(n.Object)
	case *ast.IndexExpr:
		c.collectTargetOperands(n.Object)
		c.collectExpr(n.Index)
		c.collectExpr(n.Fallback)
	case *ast.SliceExpr:
		c.collectTargetOperands(n.Object)
		c.collectExpr(n.Start)
		c.collectExpr(n.End)
	case *ast.ParenExpr:
		c.collectTargetOperands(n.Inner)
	}
}
