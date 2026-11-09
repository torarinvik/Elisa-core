package semantic

import "elisacore/src/ast"

// permissionFunctionDecls includes implementation members; they are executable
// functions with the same authority obligations as top-level declarations.
func permissionFunctionDecls(decls []scopedDecl) []scopedDecl {
	var functions []scopedDecl
	for _, scoped := range decls {
		switch declaration := scoped.Decl.(type) {
		case *ast.FuncDecl:
			functions = append(functions, scoped)
		case *ast.ImplDecl:
			for _, member := range declaration.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					nested := scoped
					nested.Decl = fn
					functions = append(functions, nested)
				}
			}
		}
	}
	return functions
}

func (a *Analyzer) inferFunctionPermissionEffects(decls []scopedDecl, appended ...map[*ast.FuncDecl]*FuncType) {
	fdecls := permissionFunctionDecls(decls)
	r := a.retained
	if r != nil && r.rbUpdating && r.perms.solveIncremental(a, fdecls, appended...) {
		return
	}
	if r != nil {
		// A full solve starts from the declaration phase's rows. During an update the
		// retained rows are the last solution, and a lost effect would survive a call
		// cycle, so every unedited row restarts from its initial one.
		r.perms.resetToInitialRows(a, fdecls, r.rbUpdating)
	}
	if r == nil {
		a.inferFunctionPermissionEffectsWorklist(fdecls)
	} else {
		// The solver already visits every equation and observes its reads. Keep
		// that conservative union instead of traversing every body again solely
		// to rebuild the retained graph. Budget exhaustion retains the old final
		// observation pass; incomplete traces never replace that behavior.
		a.inferFunctionPermissionEffectsWorklist(fdecls, &r.perms)
	}
	if r != nil {
		if !a.permissionEffectsOutcome.Converged {
			r.perms.recordGraph(a, fdecls)
		}
		r.perms.solvedIn = false
	}
}

// recomputeFunctionPermissionEffects updates all rows published by one function.
// The worklist and the test reference sweep use the same row equations.
func (a *Analyzer) recomputeFunctionPermissionEffects(scoped scopedDecl) (*FuncType, bool) {
	var result *FuncType
	changed := false
	fn, ok := scoped.Decl.(*ast.FuncDecl)
	if !ok {
		return nil, false
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
		if a.lookupTrace != nil {
			saved := a.lookupTraceOwner
			a.lookupTraceOwner = fnType.Name
			if a.lookupTraceOwner == "" {
				a.lookupTraceOwner = joinQualifiedName(scoped.Namespace, fn.Name)
			}
			defer func() { a.lookupTraceOwner = saved }()
		}
		result = fnType
		usedRefs := a.collectFunctionPermissionRefs(fn)
		mutableCollector := permissionEffectCollector{analyzer: a, mutableGlobalsOnly: true, returnType: fnType.Return}
		mutableCollector.collectStmts(fn.Body)
		mutableRefs := mutableCollector.refs()
		returnedGlobalRefs := canonicalizePermissionRefs(mutableCollector.returnedFunctionGlobalRefs)
		if !samePermissionRefs(fnType.ReturnedFunctionGlobalPermissionRefs, returnedGlobalRefs) {
			fnType.ReturnedFunctionGlobalPermissionRefs = returnedGlobalRefs
			changed = true
		}
		returnedRefs := canonicalizePermissionRefs(mutableCollector.returnedFunctionRefs)
		if !samePermissionRefs(fnType.ReturnedFunctionPermissionRefs, returnedRefs) {
			fnType.ReturnedFunctionPermissionRefs = returnedRefs
			changed = true
		}
		if !samePermissionRefs(fnType.MutableGlobalPermissionRefs, mutableRefs) {
			fnType.MutableGlobalPermissionRefs = mutableRefs
			changed = true
		}
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
	return result, changed
}

func (a *Analyzer) collectFunctionPermissionRefs(fn *ast.FuncDecl) []ast.PermissionRef {
	if fn == nil {
		return nil
	}
	// A ghost function is erased: its body never runs, so nothing in it is a runtime effect.
	if fn.IsGhost {
		return nil
	}
	collector := permissionEffectCollector{analyzer: a}
	if sym, ok := a.symbolForFuncDecl(fn); ok {
		if ft, ok := sym.Type.(*FuncType); ok {
			collector.returnType = ft.Return
		}
	}
	// docs/126 §3: a moved-in drop-typed parameter is released by this frame, so its
	// destructor's effects belong to this function's own row.
	collector.addRefs(a.implicitDropPermissionRefs(fn))
	collector.collectStmts(fn.Body)
	return collector.refs()
}

type permissionEffectCollector struct {
	returnedFunctionRefs       []ast.PermissionRef
	returnedFunctionGlobalRefs []ast.PermissionRef
	analyzer                   *Analyzer
	seen                       []ast.PermissionRef
	mutableGlobalsOnly         bool
	returnType                 Type
}

func (c *permissionEffectCollector) refs() []ast.PermissionRef {
	return canonicalizePermissionRefs(c.seen)
}

// withoutAcknowledgmentRefs drops acknowledgment-only permissions (ComplexFlow, Scalar)
// from a grant's refs: they exist purely to silence diagnostics for the code they
// lexically cover and never propagate to callers.
func withoutAcknowledgmentRefs(refs []ast.PermissionRef) []ast.PermissionRef {
	out := make([]ast.PermissionRef, 0, len(refs))
	for _, ref := range refs {
		if ref.Name == "ComplexFlow" || ref.Name == "Scalar" {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func (c *permissionEffectCollector) addRefs(refs []ast.PermissionRef) {
	if len(refs) == 0 {
		return
	}
	if c.mutableGlobalsOnly {
		for _, ref := range refs {
			if isGlobalPermissionRef(ref) {
				c.seen = append(c.seen, ref)
			}
		}
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
		c.addRefs(c.analyzer.mutableGlobalBorrowRefs[n])
		if n.Value != nil {
			c.collectExpr(n.Value)
		}
		if c.analyzer.enforceUnsafePermissions && c.analyzer.stmtRequiresUnsafeAlias(n) {
			c.addRefs(unsafeAliasRefs(n.Position))
		}
		// docs/126 §3: a value this scope can implicitly drop charges its destructor's
		// effects here, exactly as a written call to it would. This runs inside the
		// whole-program fixpoint, so a `__drop__` whose own effects are themselves
		// inferred settles before its holders do.
		c.addRefs(c.analyzer.implicitDropPermissionRefs(n))
	case *ast.LetDestructureStmt:
		c.collectExpr(n.Value)
	case *ast.TupleBindStmt:
		c.collectExpr(n.Value)
	case *ast.MoveBindStmt:
		c.collectExpr(n.Value)
		if n.Store != nil {
			c.collectExpr(n.Store)
		}
	case *ast.DeferStmt:
		c.collectStmts(n.Body)
	case *ast.AssignStmt:
		// A scalar assignment through a reference reads the reference value from
		// its storage before writing the referent. `collectWriteTarget` accounts
		// for that read when the reference slot itself is an immutable global;
		// preserve the semantic marker here for mutable global reference slots too.
		c.collectWriteTarget(n.Target, n.WriteThrough)
		c.addRefs(c.analyzer.mutableGlobalBorrowAssignRefs[n])
		c.collectExpr(n.Value)
		if c.analyzer.enforceUnsafePermissions && c.analyzer.stmtRequiresUnsafeAlias(n) {
			c.addRefs(unsafeAliasRefs(n.Position))
		}
	case *ast.AugAssignStmt:
		if n.CollectionAppend != nil {
			c.collectExpr(n.CollectionAppend)
			return
		}
		c.collectWriteTarget(n.Target, true)
		c.collectExpr(n.Value)
	case *ast.AsRefAssignStmt:
		c.collectWriteTarget(n.Target, false)
		c.collectExpr(n.Value)
	case *ast.ReturnStmt:
		if _, returnsCallback := c.returnType.(*FuncType); returnsCallback {
			if callback, ok := c.analyzer.exprType(n.Value).(*FuncType); ok {
				c.returnedFunctionRefs = append(c.returnedFunctionRefs, c.analyzer.effectiveFunctionPermissionRefs(callback)...)
				c.returnedFunctionGlobalRefs = append(c.returnedFunctionGlobalRefs, c.analyzer.mandatoryGlobalPermissionRefs(callback)...)
			}
		}
		c.addRefs(c.analyzer.mutableGlobalReturnedRefRefs(n.Value, c.returnType))
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
			c.collectExpr(arm.Guard)
			c.collectStmts(arm.Body)
		}
	case *ast.InStoreStmt:
		if root := globalReferenceStorageExpr(n.Store); root != nil {
			if _, global := c.analyzer.mutableGlobalStorageRoot(root); global {
				c.addRefs(globalWriteRefs(root.Pos()))
			}
		}
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
			c.collectWithGrantedRefs(trustedUnsafePermissionRefs(c.analyzer.resolvePermissionRefs(n.Permissions, false)), func() {
				c.collectStmts(n.Body)
			})
			break
		}
		// Acknowledgment-only grants (ComplexFlow, Scalar) silence local diagnostics;
		// nothing ever REQUIRES them, so they must not join the function's inferred
		// effect row (they would otherwise cascade `can[ComplexFlow]` obligations up
		// every caller chain — see registerBuiltinPermission's contract for both).
		c.addRefs(withoutAcknowledgmentRefs(c.analyzer.resolvePermissionRefs(n.Permissions, false)))
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
		c.addRefs(c.analyzer.mutableGlobalStoreRowsReadRefs(n.Source))
		c.addRefs(c.analyzer.mutableGlobalStoreRowsWriteRefs(n.Source))
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
