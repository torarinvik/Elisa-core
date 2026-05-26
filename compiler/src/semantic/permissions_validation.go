package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

func (a *Analyzer) validatePermissionUsage(decls []scopedDecl) {
	for _, scoped := range decls {
		fn, ok := scoped.Decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			a.validateFunctionPermissionUsage(fn)
		})
	}
}

func (a *Analyzer) validateFunctionPermissionUsage(fn *ast.FuncDecl) {
	granted := map[string]bool{}
	savedFuncType := a.currentFuncType
	defer func() { a.currentFuncType = savedFuncType }()
	if sym, ok := a.symbolForFuncDecl(fn); ok {
		if fnType, ok := sym.Type.(*FuncType); ok && fnType != nil {
			a.currentFuncType = fnType
		}
	}
	a.validatePermissionStmts(fn.Body, granted)
}

func grantedPermissionFamilies(families []string) map[string]bool {
	granted := make(map[string]bool, len(families))
	for _, family := range families {
		granted[family] = true
	}
	return granted
}

func missingGrantedPermissionFamilies(refs []ast.PermissionRef, granted map[string]bool) []string {
	missingRefs := missingGrantedPermissionRefs(refs, granted)
	if len(missingRefs) == 0 {
		return nil
	}
	missing := make([]string, 0, len(missingRefs))
	seen := map[string]bool{}
	for _, ref := range missingRefs {
		if ref.Name == "" || seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		missing = append(missing, ref.Name)
	}
	return missing
}

func (a *Analyzer) warnOnMissingLocalGrant(pos lexer.Pos, label string, refs []ast.PermissionRef, granted map[string]bool) {
	missing := missingGrantedPermissionFamilies(refs, granted)
	if len(missing) == 0 {
		return
	}
	msg := effectAuthorityGrantMessage(label, missing, permissionGrantHint(refs, missing))
	// Address-space crossings (Unsafe.GuestHostPointerCast) are the strongest
	// memory-safety boundary: converting a guest address carrier into a host
	// pointer is ALWAYS a hard error when ungranted, even when general unsafe
	// enforcement is in warning mode. This guarantees a produced or received
	// guest address can never be silently dereferenced as a host pointer; the
	// author must opt in with `trusted Unsafe.GuestHostPointerCast`.
	for _, ref := range missingGrantedPermissionRefs(refs, granted) {
		if ref.Name == "Unsafe" && ref.Member == "GuestHostPointerCast" {
			a.errorf(pos, "%s", msg)
			return
		}
	}
	// Memory-safety opt-outs (the Unsafe.* family) are HARD ERRORS under
	// enforcement: a memory-unsafe operation must not compile unless the author
	// explicitly grants the corresponding Unsafe permission (the "guaranteed
	// memory safety unless you explicitly opt out" contract, docs/26). Effect
	// authority (Abort/Memory/...) stays a warning.
	if a.enforceUnsafePermissions && allUnsafeFamilies(missing) {
		a.errorf(pos, "%s", msg)
		return
	}
	a.warnf(pos, msg)
}

// allUnsafeFamilies reports whether every missing permission family is the
// Unsafe family, i.e. the missing grants are all memory-safety opt-outs.
func allUnsafeFamilies(families []string) bool {
	if len(families) == 0 {
		return false
	}
	for _, family := range families {
		if family != "Unsafe" {
			return false
		}
	}
	return true
}

func (a *Analyzer) warnOnRedundantLocalGrant(pos lexer.Pos, label string, refs []ast.PermissionRef, granted map[string]bool) {
	refs = canonicalizePermissionRefs(refs)
	if len(refs) == 0 {
		return
	}
	if len(missingGrantedPermissionRefs(refs, granted)) != 0 {
		return
	}
	hint := strings.TrimSpace(permissionGrantHint(refs, permissionFamiliesFromRefs(refs)))
	a.warnf(pos, "%s grants %s redundantly; a surrounding can ...: block already grants these effects", label, hint)
}

func (a *Analyzer) validatePermissionStmts(stmts []ast.Stmt, granted map[string]bool) {
	for _, stmt := range stmts {
		a.validatePermissionStmt(stmt, granted)
	}
}

func (a *Analyzer) validatePermissionStmt(stmt ast.Stmt, granted map[string]bool) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		if n.Value != nil {
			a.validatePermissionExpr(n.Value, granted)
		}
		if a.enforceUnsafePermissions && a.stmtRequiresUnsafeAlias(n) {
			a.warnOnMissingLocalGrant(n.Pos(), "mutable alias", unsafeAliasRefs(n.Position), granted)
		}
	case *ast.LetDestructureStmt:
		a.validatePermissionExpr(n.Value, granted)
	case *ast.TupleBindStmt:
		a.validatePermissionExpr(n.Value, granted)
	case *ast.MoveBindStmt:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
		}
	case *ast.ArgsScopeStmt:
		for _, arg := range n.Args {
			a.validatePermissionExpr(arg.Value, granted)
		}
		for _, pack := range n.ParamPacks {
			for _, arg := range pack.Args {
				a.validatePermissionExpr(arg.Value, granted)
			}
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.DeferStmt:
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.AssignStmt:
		a.validatePermissionExpr(n.Target, granted)
		a.validatePermissionExpr(n.Value, granted)
		if a.enforceUnsafePermissions && a.stmtRequiresUnsafeAlias(n) {
			a.warnOnMissingLocalGrant(n.Pos(), "mutable alias", unsafeAliasRefs(n.Position), granted)
		}
	case *ast.AugAssignStmt:
		a.validatePermissionExpr(n.Target, granted)
		a.validatePermissionExpr(n.Value, granted)
	case *ast.AsRefAssignStmt:
		a.validatePermissionExpr(n.Target, granted)
		a.validatePermissionExpr(n.Value, granted)
	case *ast.ReturnStmt:
		if n.Value != nil {
			a.validatePermissionExpr(n.Value, granted)
		}
	case *ast.IfStmt:
		a.validatePermissionExpr(n.Cond, granted)
		a.validatePermissionStmts(n.Then, cloneGrantedPermissionFamilies(granted))
		for _, elif := range n.Elifs {
			a.validatePermissionExpr(elif.Cond, granted)
			a.validatePermissionStmts(elif.Body, cloneGrantedPermissionFamilies(granted))
		}
		a.validatePermissionStmts(n.Else, cloneGrantedPermissionFamilies(granted))
	case *ast.MatchStmt:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
		}
		for _, arm := range n.Arms {
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.InStoreStmt:
		a.validatePermissionExpr(n.Store, granted)
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.CanStmt:
		refs := a.resolvePermissionRefs(n.Permissions, false)
		if !n.SuppressPermissionInference {
			a.warnOnRedundantLocalGrant(n.Pos(), "can block", refs, granted)
		}
		a.validatePermissionStmts(n.Body, extendGrantedPermissionRefs(granted, refs))
	case *ast.SignalStmt:
		refs := a.resolvePermissionRefs(n.Permissions, false)
		a.warnOnMissingLocalGrant(n.Pos(), "signal", refs, granted)
	case *ast.PoolStmt:
		a.validatePermissionExpr(n.Workers, granted)
		if !granted["Pool"] {
			refs := []ast.PermissionRef{{Position: n.Position, Name: "Pool", Member: "Create"}, {Position: n.Position, Name: "Pool", Member: "Shutdown"}}
			a.warnOnMissingLocalGrant(n.Pos(), "pool scope", refs, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.LockStmt:
		a.validatePermissionExpr(n.Mutex, granted)
		if !granted["Sync"] {
			refs := []ast.PermissionRef{{Position: n.Position, Name: "Sync", Member: "Lock"}, {Position: n.Position, Name: "Sync", Member: "Unlock"}}
			a.warnOnMissingLocalGrant(n.Pos(), "lock scope", refs, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.WhileStmt:
		a.validatePermissionExpr(n.Cond, granted)
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.ForStmt:
		a.validatePermissionExpr(n.Start, granted)
		a.validatePermissionExpr(n.End, granted)
		if n.Step != nil {
			a.validatePermissionExpr(n.Step, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.IterForStmt:
		a.validatePermissionExpr(n.Source, granted)
		a.validatePermissionExpr(n.WhereFilter, granted)
		a.validatePermissionExpr(n.Filter, granted)
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.ParallelForStmt:
		a.validatePermissionExpr(n.Source, granted)
		if !granted["Pool"] {
			refs := []ast.PermissionRef{{Position: n.Position, Name: "Pool", Member: "Submit"}, {Position: n.Position, Name: "Pool", Member: "WaitAll"}}
			a.warnOnMissingLocalGrant(n.Pos(), "parallel for", refs, granted)
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
	case *ast.PanicStmt:
		a.warnOnMissingLocalGrant(n.Pos(), "panic", []ast.PermissionRef{{Position: n.Position, Name: "Abort", Member: "Panic"}}, granted)
		a.validatePermissionExpr(n.Message, granted)
	case *ast.ExprStmt:
		a.validatePermissionExpr(n.Expr, granted)
	case *ast.StaticIfStmt:
		for _, active := range a.activeStmtBranch(n) {
			a.validatePermissionStmt(active, granted)
		}
	case *ast.StaticErrorStmt:
		a.validatePermissionExpr(n.Message, granted)
	case *ast.StaticAssertStmt:
		a.validatePermissionExpr(n.Cond, granted)
		if n.Message != nil {
			a.validatePermissionExpr(n.Message, granted)
		}
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			a.validatePermissionExpr(item.Cond, granted)
			if item.Message != nil {
				a.validatePermissionExpr(item.Message, granted)
			}
		}
	case *ast.StaticBlockStmt:
		for _, inner := range n.Body {
			a.validatePermissionStmt(inner, granted)
		}
	case *ast.DiscardStmt:
		a.validatePermissionExpr(n.Value, granted)
	}
}

func (a *Analyzer) validatePermissionExpr(expr ast.Expr, granted map[string]bool) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.Ident:
		if a.enforceUnsafePermissions {
			if sym, _, ok := a.lookupVisibleGlobal(n.Name); ok && sym.Kind == SymbolGlobal && sym.Mutable {
				a.warnOnMissingLocalGrant(n.Pos(), "mutable global access", unsafeMutableGlobalRefs(n.Position), granted)
			}
			if a.storageViewUseRequiresUnsafeStaleRef(n) {
				a.warnOnMissingLocalGrant(n.Pos(), "stale reference", unsafeStaleRefRefs(n.Position), granted)
			}
		}
	case *ast.BinaryExpr:
		a.validatePermissionExpr(n.Left, granted)
		a.validatePermissionExpr(n.Right, granted)
		if a.enforceUnsafePermissions && binaryExprRequiresUnsafePointerArithmetic(n.Op, a.exprTypes[n.Left], a.exprTypes[n.Right]) {
			a.warnOnMissingLocalGrant(n.Pos(), "pointer arithmetic", unsafePointerArithmeticRefs(n.Position), granted)
		}
	case *ast.UnaryExpr:
		a.validatePermissionExpr(n.Operand, granted)
	case *ast.CallExpr:
		a.validatePermissionExpr(n.Func, granted)
		a.validatePermissionExpr(n.SafeReceiver, granted)
		for _, arg := range n.Args {
			a.validatePermissionExpr(arg, granted)
		}
		if a.enforceUnsafePermissions && a.exprRequiresUnsafeAlias(n) {
			a.warnOnMissingLocalGrant(n.Pos(), "mutable alias", unsafeAliasRefs(n.Position), granted)
		}
		if a.enforceUnsafePermissions && isIndirectCallTarget(n.Func) {
			a.warnOnMissingLocalGrant(n.Pos(), "indirect call", unsafeIndirectCallRefs(n.Position), granted)
		}
		a.validateCallPermissions(n.Position, n.Func, granted)
		if a.enforceUnsafePermissions {
			if fnType, ok := a.exprTypes[n.Func].(*FuncType); ok {
				if fnType.Name == "spawn1" && len(n.Args) > 1 {
					if threadTransferRequiresUnsafeThreadShare(a.exprTypes[n.Args[1]], map[string]bool{}) {
						a.warnOnMissingLocalGrant(n.Args[1].Pos(), "thread share", unsafeThreadShareRefs(n.Args[1].Pos()), granted)
					}
				}
				if fnType.Name == "pool_submit1" && len(n.Args) > 2 {
					if threadTransferRequiresUnsafeThreadShare(a.exprTypes[n.Args[2]], map[string]bool{}) {
						a.warnOnMissingLocalGrant(n.Args[2].Pos(), "thread share", unsafeThreadShareRefs(n.Args[2].Pos()), granted)
					}
				}
				if resultPayload, ok := threadTransferResultPayloadType(fnType.Name, fnType.Return); ok && threadTransferRequiresUnsafeThreadShare(resultPayload, map[string]bool{}) {
					a.warnOnMissingLocalGrant(n.Pos(), "thread share", unsafeThreadShareRefs(n.Position), granted)
				}
			}
		}
	case *ast.FieldExpr:
		a.validatePermissionExpr(n.Object, granted)
	case *ast.IndexExpr:
		a.validatePermissionExpr(n.Object, granted)
		a.validatePermissionExpr(n.Index, granted)
		a.validatePermissionExpr(n.Fallback, granted)
		if a.enforceUnsafePermissions && a.indexExprRequiresUncheckedIndexPermission(n) {
			a.warnOnMissingLocalGrant(n.Pos(), "unchecked index", unsafeUncheckedIndexRefs(n.Position), granted)
		}
	case *ast.SliceExpr:
		a.validatePermissionExpr(n.Object, granted)
		a.validatePermissionExpr(n.Start, granted)
		a.validatePermissionExpr(n.End, granted)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			a.validatePermissionExpr(elem, granted)
		}
		a.validatePermissionExpr(n.Owner, granted)
	case *ast.CastExpr:
		a.validatePermissionExpr(n.Operand, granted)
		src := a.exprTypes[n.Operand]
		dst := a.exprTypes[n]
		if n.Origin != ast.CastExprOriginIndirectCall && a.castRequiresUnsafeGuestHostPointerCast(n, dst) {
			// Address-space crossing: checked regardless of general unsafe
			// enforcement. warnOnMissingLocalGrant hard-errors a missing
			// Unsafe.GuestHostPointerCast, so a guest address can never be
			// silently dereferenced as a host pointer.
			a.warnOnMissingLocalGrant(n.Pos(), "guest-host pointer cast", unsafeGuestHostPointerCastRefs(n.Position), granted)
		} else if a.enforceUnsafePermissions && n.Origin != ast.CastExprOriginIndirectCall && castRequiresUnsafePointerCast(src, dst) {
			a.warnOnMissingLocalGrant(n.Pos(), "pointer cast", unsafePointerCastRefs(n.Position), granted)
		} else if a.enforceUnsafePermissions && a.unsafeLifetimeWidenCasts[n] {
			a.warnOnMissingLocalGrant(n.Pos(), "lifetime-widening reference cast (a borrow cast to a longer-lived storage class; it can dangle when the storage is freed — persist via clone[dstr] instead)", unsafePointerCastRefs(n.Position), granted)
		}
		if a.enforceUnsafePermissions && a.unsafeBufferReinterpretCasts[n] {
			a.warnOnMissingLocalGrant(n.Pos(), "buffer reinterpret cast", unsafeBufferReinterpretRefs(n.Position), granted)
		}
		if sym, ok := a.resolvedCastHooks[n]; ok {
			if fnType, ok := sym.Type.(*FuncType); ok {
				a.validateRequiredPermissions(n.Position, fnType, granted)
			}
		}
	case *ast.TernaryExpr:
		a.validatePermissionExpr(n.Value, granted)
		a.validatePermissionExpr(n.Cond, granted)
		a.validatePermissionExpr(n.Alt, granted)
	case *ast.AddrOfExpr:
		a.validatePermissionExpr(n.Operand, granted)
	case *ast.MoveExpr:
		a.validatePermissionExpr(n.Operand, granted)
	case *ast.SpecializeExpr:
		a.validatePermissionExpr(n.Operand, granted)
	case *ast.StructLitExpr:
		if call, ok := a.loweredInitCalls[n]; ok && call != nil {
			a.validatePermissionExpr(call, granted)
			break
		}
		for _, arg := range n.Args {
			a.validatePermissionExpr(arg, granted)
		}
	case *ast.RecordUpdateExpr:
		a.validatePermissionExpr(n.Base, granted)
		for _, arg := range n.Args {
			a.validatePermissionExpr(arg, granted)
		}
	case *ast.TupleExpr:
		for _, elem := range n.Elems {
			a.validatePermissionExpr(elem, granted)
		}
	case *ast.ParenExpr:
		a.validatePermissionExpr(n.Inner, granted)
	case *ast.RaiseExpr:
		a.validatePermissionExpr(n.Error, granted)
	case *ast.TryExpr:
		a.validatePermissionExpr(n.Value, granted)
		if n.Fallback != nil {
			a.validatePermissionExpr(n.Fallback, granted)
		}
	case *ast.UnwrapElseExpr:
		a.validatePermissionExpr(n.Value, granted)
		a.validatePermissionExpr(n.Fallback, granted)
	case *ast.OptionalBindExpr:
		a.validatePermissionExpr(n.Value, granted)
	case *ast.AllocExpr:
		if n.Owner != nil {
			a.validatePermissionExpr(n.Owner, granted)
		}
		a.validatePermissionExpr(n.NodeSpan, granted)
		a.validatePermissionExpr(n.Value, granted)
	case *ast.CanExpr:
		refs := a.resolvePermissionRefs(n.Permissions, false)
		if !n.SuppressPermissionInference {
			a.warnOnRedundantLocalGrant(n.Pos(), "inline can", refs, granted)
		}
		a.validatePermissionExpr(n.Expr, extendGrantedPermissionRefs(granted, refs))
	case *ast.MatchExpr:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
		}
		for _, arm := range n.Arms {
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.VisitExpr:
		a.validatePermissionExpr(n.Value, granted)
		for _, arm := range n.Arms {
			a.validatePermissionExpr(arm.Guard, granted)
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.FoldExpr:
		a.validatePermissionExpr(n.Value, granted)
		for _, arm := range n.Arms {
			a.validatePermissionExpr(arm.Guard, granted)
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			a.validatePermissionExpr(target, granted)
		}
	case *ast.IsAliasExpr:
		a.validatePermissionExpr(n.Target, granted)
	}
}

func (a *Analyzer) validateCallPermissions(pos lexer.Pos, fnExpr ast.Expr, granted map[string]bool) {
	fnType, ok := a.exprTypes[fnExpr].(*FuncType)
	if !ok {
		return
	}
	a.validateRequiredPermissions(pos, fnType, granted)
}

func (a *Analyzer) validateRequiredPermissions(pos lexer.Pos, fnType *FuncType, granted map[string]bool) {
	if fnType == nil || len(fnType.Permissions) == 0 {
		return
	}
	if a.permissionWarningsSuppressedByGenericContext(fnType, granted) {
		return
	}
	missingRefs := missingGrantedPermissionRefs(functionPermissionRefs(fnType), granted)
	for _, ref := range missingRefs {
		if ref.Name == "Unsafe" && ref.Member == "SegmentMutation" {
			a.errorf(pos, "%s", effectAuthorityGrantMessage("call to "+quoteFactTarget(fnType.Name), []string{"Unsafe"}, "can Unsafe.SegmentMutation"))
			return
		}
		if ref.Name == "Unsafe" && ref.Member == "GuestSegmentInstall" {
			a.errorf(pos, "%s", effectAuthorityGrantMessage("call to "+quoteFactTarget(fnType.Name), []string{"Unsafe"}, "can Unsafe.GuestSegmentInstall"))
			return
		}
	}
	missing := missingGrantedPermissionFamilies(functionPermissionRefs(fnType), granted)
	if len(missing) == 0 {
		return
	}
	a.warnf(pos, effectAuthorityGrantMessage("call to "+quoteFactTarget(fnType.Name), missing, permissionGrantHint(functionPermissionRefs(fnType), missing)))
}
