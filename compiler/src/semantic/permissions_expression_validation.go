package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) validatePermissionExpr(expr ast.Expr, granted map[string]bool) {
	a.validatePermissionExprWithValueContext(expr, granted, true)
}

func (a *Analyzer) validatePermissionExprWithValueContext(expr ast.Expr, granted map[string]bool, wholeValue bool) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.Ident:
		_, mutableGlobal := a.mutableGlobalStorageRoot(n)
		wholeMutableGlobal := !mutableGlobal && wholeValue && a.globalWholeValueReadRequiresGrant(n)
		if mutableGlobal || wholeMutableGlobal {
			a.errorOnMissingLocalGrant(n.Pos(), "mutable global read", globalReadRefs(n.Position), granted)
		}
		if sym, ok := a.globalStorageRoot(n); ok {
			if a.enforceUnsafePermissions && sym.Kind == SymbolGlobal && sym.Mutable {
				a.warnOnMissingLocalGrant(n.Pos(), "mutable global access", unsafeMutableGlobalRefs(n.Position), granted)
			}
		}
		if a.enforceUnsafePermissions {
			if a.storageViewUseRequiresUnsafeStaleRef(n) {
				a.warnOnMissingLocalGrant(n.Pos(), "stale reference", unsafeStaleRefRefs(n.Position), granted)
			}
		}
	case *ast.BinaryExpr:
		a.validatePermissionExpr(n.Left, granted)
		a.validatePermissionExpr(n.Right, granted)
		if a.enforceUnsafePermissions && binaryExprRequiresUnsafePointerArithmetic(n.Op, a.exprType(n.Left), a.exprType(n.Right)) {
			a.warnOnMissingLocalGrant(n.Pos(), "pointer arithmetic", unsafePointerArithmeticRefs(n.Position), granted)
		}
	case *ast.UnaryExpr:
		a.validatePermissionExpr(n.Operand, granted)
	case *ast.CallExpr:
		a.errorOnMissingLocalGrant(n.Pos(), "mutable global write", a.mutableGlobalCallWriteRefs(n), granted)
		a.errorOnMissingLocalGrant(n.Pos(), "callback argument", a.mutableGlobalCallbackRefs(n), granted)
		for _, arg := range n.LoweredArgs() {
			if callback, ok := a.exprType(arg).(*FuncType); ok {
				a.validateLocalPermissionRowGrant(n.Pos(), "callback argument", functionPermissionRefs(callback), granted)
			}
		}
		a.validatePermissionExpr(n.Func, granted)
		a.validatePermissionExpr(n.SafeReceiver, granted)
		for _, arg := range n.LoweredArgs() {
			a.validatePermissionExpr(arg, granted)
		}
		if a.exprRequiresUnsafeAlias(n) {
			a.errorOnMissingLocalGrant(n.Pos(), "mutable alias", unsafeAliasRefs(n.Position), granted)
		}
		if a.enforceUnsafePermissions && isIndirectCallTarget(n.Func) {
			a.warnOnMissingLocalGrant(n.Pos(), "indirect call", unsafeIndirectCallRefs(n.Position), granted)
		}
		a.validateCallPermissions(n.Position, n.Func, granted, n.SafeConcurrencySugar)
		if a.enforceUnsafePermissions {
			if fnType, ok := a.exprType(n.Func).(*FuncType); ok {
				if (fnType.Name == "spawn1" || fnType.Name == "spawn_daemon") && len(n.Args) > 1 {
					if threadTransferRequiresUnsafeThreadShare(a.exprType(n.Args[1]), map[string]bool{}) {
						a.warnOnMissingLocalGrant(n.Args[1].Pos(), "thread share", unsafeThreadShareRefs(n.Args[1].Pos()), granted)
					}
				}
				if fnType.Name == "pool_submit1" && len(n.Args) > 2 {
					if threadTransferRequiresUnsafeThreadShare(a.exprType(n.Args[2]), map[string]bool{}) {
						a.warnOnMissingLocalGrant(n.Args[2].Pos(), "thread share", unsafeThreadShareRefs(n.Args[2].Pos()), granted)
					}
				}
				if resultPayload, ok := threadTransferResultPayloadType(fnType.Name, fnType.Return); ok && threadTransferRequiresUnsafeThreadShare(resultPayload, map[string]bool{}) {
					a.warnOnMissingLocalGrant(n.Pos(), "thread share", unsafeThreadShareRefs(n.Position), granted)
				}
			}
		}
	case *ast.FieldExpr:
		if a.globalProjectionBeginsMutablePath(n) || (wholeValue && a.globalWholeValueReadRequiresGrant(n)) {
			a.errorOnMissingLocalGrant(n.Pos(), "mutable global read", globalReadRefs(n.Position), granted)
		}
		a.validatePermissionExprWithValueContext(n.Object, granted, false)
	case *ast.IndexExpr:
		if a.globalProjectionBeginsMutablePath(n) || (wholeValue && a.globalWholeValueReadRequiresGrant(n)) {
			a.errorOnMissingLocalGrant(n.Pos(), "mutable global read", globalReadRefs(n.Position), granted)
		}
		a.validatePermissionExprWithValueContext(n.Object, granted, false)
		a.validatePermissionExpr(n.Index, granted)
		a.validatePermissionExpr(n.Fallback, granted)
		if a.enforceUnsafePermissions && a.indexExprRequiresUncheckedIndexPermission(n) {
			a.warnOnMissingLocalGrant(n.Pos(), "unchecked index", unsafeUncheckedIndexRefs(n.Position), granted)
		}
	case *ast.SliceExpr:
		if a.globalProjectionBeginsMutablePath(n) || (wholeValue && a.globalWholeValueReadRequiresGrant(n)) {
			a.errorOnMissingLocalGrant(n.Pos(), "mutable global read", globalReadRefs(n.Position), granted)
		}
		a.validatePermissionExprWithValueContext(n.Object, granted, false)
		a.validatePermissionExpr(n.Start, granted)
		a.validatePermissionExpr(n.End, granted)
	case *ast.ListLitExpr:
		for _, elem := range n.Elems {
			a.validatePermissionExpr(elem, granted)
		}
		a.validatePermissionExpr(n.Owner, granted)
	case *ast.CastExpr:
		if call, ok := a.postfixShorthandCalls[n]; ok && call != nil {
			a.validatePermissionExpr(call, granted)
			break
		}
		a.validatePermissionExprWithValueContext(n.Operand, granted, wholeValue)
		src := a.exprType(n.Operand)
		dst := a.exprType(n)
		if n.Origin != ast.CastExprOriginIndirectCall && a.castRequiresUnsafeGuestHostPointerCast(n, dst) {
			// Address-space crossing: checked regardless of general unsafe
			// enforcement. warnOnMissingLocalGrant hard-errors a missing
			// Unsafe.GuestHostPointerCast, so a guest address can never be
			// silently dereferenced as a host pointer.
			a.warnOnMissingLocalGrant(n.Pos(), "guest-host pointer cast", unsafeGuestHostPointerCastRefs(n.Position), granted)
		} else if kind := a.castForgesOrUpgradesReference(n, src, dst); n.Origin != ast.CastExprOriginIndirectCall && kind != "" {
			// Inventing provenance or write capability is never a default-mode warning: it
			// hard-errors without an Unsafe.PointerCast grant in every mode.
			a.errorOnMissingLocalGrant(n.Pos(), referenceAuthorityCastLabel(kind), unsafePointerCastRefs(n.Position), granted)
		} else if n.Origin != ast.CastExprOriginIndirectCall && castRequiresUnsafePointerCast(src, dst) {
			// Memory-safety opt-out: hard-errors under enforcement, warns in permissive
			// mode (warnOnMissingLocalGrant picks the severity). Surfaced in BOTH modes so
			// an ungranted unsafe cast is never silent — "make unsafe loud" (docs/26).
			a.warnOnMissingLocalGrant(n.Pos(), "pointer cast", unsafePointerCastRefs(n.Position), granted)
		} else if a.unsafeLifetimeWidenCasts[n] {
			a.warnOnMissingLocalGrant(n.Pos(), "lifetime-widening reference cast (a borrow cast to a longer-lived storage class; it can dangle when the storage is freed — persist via clone[dstr] instead)", unsafePointerCastRefs(n.Position), granted)
		}
		if a.unsafeBufferReinterpretCasts[n] {
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
	case *ast.ListComprehensionExpr:
		a.errorOnMissingLocalGrant(n.Pos(), "mutable global read", a.mutableGlobalStoreRowsReadRefs(n.Source), granted)
		for _, b := range n.Bindings {
			if vd, ok := b.(*ast.VarDeclStmt); ok {
				a.validatePermissionStmt(vd, granted)
			}
		}
		a.validatePermissionExpr(n.Key, granted)
		a.validatePermissionExpr(n.Value, granted)
		a.validatePermissionExpr(n.Source, granted)
		a.validatePermissionExpr(n.RangeEnd, granted)
		a.validatePermissionExpr(n.RangeStep, granted)
		a.validatePermissionExpr(n.Filter, granted)
		a.validatePermissionExpr(n.Owner, granted)
	case *ast.QueryExpr:
		a.errorOnMissingLocalGrant(n.Pos(), "mutable global read", a.mutableGlobalStoreRowsReadRefs(n.Source), granted)
		a.validatePermissionExpr(n.Source, granted)
		a.validatePermissionExpr(n.Filter, granted)
		a.validatePermissionExpr(n.Projection, granted)
		a.validatePermissionExpr(n.Owner, granted)
	case *ast.ExprBlock:
		// A value block is an executable expression. Its leading statements and
		// tail value can contain calls/effects just like a function body; skipping
		// it under-reports effects when a block is used as an initializer or return.
		a.validatePermissionStmts(n.Stmts, granted)
		a.validatePermissionExpr(n.Value, granted)
	case *ast.CatchExpr:
		a.validatePermissionExpr(n.Value, granted)
		a.validatePermissionStmts(n.Success.Body, granted)
		for _, arm := range n.Arms {
			a.validatePermissionStmts(arm.Body, granted)
		}
	case *ast.LambdaExpr:
		savedFunc := a.currentFuncType
		if ft, ok := a.exprType(n).(*FuncType); ok {
			a.currentFuncType = ft
		}
		a.validatePermissionStmts(n.Body, cloneGrantedPermissionFamilies(granted))
		a.validatePermissionExpr(n.BodyExpr, granted)
		if a.currentFuncType != nil {
			a.errorOnMissingLocalGrant(n.Pos(), "mutable global write", a.mutableGlobalReturnedRefRefs(n.BodyExpr, a.currentFuncType.Return), granted)
		}
		a.currentFuncType = savedFunc
	case *ast.ParenExpr:
		a.validatePermissionExprWithValueContext(n.Inner, granted, wholeValue)
	case *ast.RaiseExpr:
		a.validatePermissionExpr(n.Error, granted)
	case *ast.TryExpr:
		a.validatePermissionExpr(n.Value, granted)
		if n.Fallback != nil {
			a.validatePermissionExpr(n.Fallback, granted)
		}
		if n.Recovery != nil {
			a.validatePermissionExpr(n.Recovery.Value, granted)
			a.validatePermissionStmts(n.Recovery.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.UnwrapElseExpr:
		a.validatePermissionExpr(n.Value, granted)
		a.validatePermissionExpr(n.Fallback, granted)
		if n.Recovery != nil {
			a.validatePermissionExpr(n.Recovery.Value, granted)
			a.validatePermissionStmts(n.Recovery.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.GetExpr:
		a.validatePermissionExpr(n.Value, granted)
		a.validatePermissionExpr(n.Fallback, granted)
		if n.Recovery != nil {
			a.validatePermissionExpr(n.Recovery.Value, granted)
			a.validatePermissionStmts(n.Recovery.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.OptionalBindExpr:
		a.validatePermissionExpr(n.Value, granted)
	case *ast.AllocExpr:
		if n.Owner != nil {
			a.validatePermissionExpr(n.Owner, granted)
		}
		a.validatePermissionExpr(n.Value, granted)
	case *ast.CanExpr:
		refs := a.resolvePermissionRefs(n.Permissions, false)
		if n.SuppressPermissionInference {
			refs = trustedUnsafePermissionRefs(refs)
		}
		if !n.SuppressPermissionInference {
			a.warnOnRedundantLocalGrant(n.Pos(), "inline can", refs, granted)
		}
		a.validatePermissionExpr(n.Expr, a.extendGrantedPermissionRefs(granted, refs))
	case *ast.MatchExpr:
		a.validatePermissionExpr(n.Value, granted)
		if n.Store != nil {
			a.validatePermissionExpr(n.Store, granted)
		}
		for _, arm := range n.Arms {
			a.validatePermissionExpr(arm.Guard, granted)
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.FoldExpr:
		a.errorOnMissingLocalGrant(n.Pos(), "mutable global read", a.mutableGlobalStoreRowsReadRefs(n.Value), granted)
		a.errorOnMissingLocalGrant(n.Pos(), "mutable global write", a.mutableGlobalFoldEmitRefs(n), granted)
		a.validatePermissionExpr(n.Value, granted)
		for _, arm := range n.Arms {
			a.validatePermissionExpr(arm.Guard, granted)
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
		}
	case *ast.EmitExpr:
		a.validatePermissionExpr(n.Value, granted)
	case *ast.MachineFromExpr:
		if n.Lowered != nil {
			a.validatePermissionExpr(n.Lowered, granted)
			break
		}
		a.errorOnMissingLocalGrant(n.Pos(), "mutable global write", a.mutableGlobalMachinePayloadRefs(n), granted)
		for _, arg := range n.StartArgs {
			a.validatePermissionExpr(arg, granted)
		}
		a.validatePermissionExpr(n.Decreases, granted)
		a.validatePermissionStmts(n.HeaderDecls, granted)
		for _, arm := range n.Arms {
			a.validatePermissionStmts(arm.Body, cloneGrantedPermissionFamilies(granted))
			for _, term := range arm.Terminators {
				a.validatePermissionExpr(term.Guard, granted)
				for _, arg := range term.Args {
					a.validatePermissionExpr(arg, granted)
				}
				a.validatePermissionExpr(term.Value, granted)
			}
		}
	case *ast.IsPatternExpr:
		for _, target := range n.Targets {
			a.validatePermissionExpr(target, granted)
		}
	case *ast.IsAliasExpr:
		a.validatePermissionExpr(n.Target, granted)
	}
}

func (a *Analyzer) validateCallPermissions(pos lexer.Pos, fnExpr ast.Expr, granted map[string]bool, safeConcurrencySugar bool) {
	fnType, ok := a.exprType(fnExpr).(*FuncType)
	if !ok {
		return
	}
	if !safeConcurrencySugar {
		a.warnOnLegacyRawConcurrencyCall(pos, fnType)
	}
	a.validateRequiredPermissions(pos, fnType, granted)
}

func (a *Analyzer) warnOnLegacyRawConcurrencyCall(pos lexer.Pos, fnType *FuncType) {
	if fnType == nil || fnType.Name == "" || isRuntimeStdPermissionInternal(pos.File) {
		return
	}
	// Raw concurrency primitives were removed from the public surface: the safe
	// APIs (nursery/pool/AtomicCell/predicate_wait/each/reduce) are the only way
	// in for user code. The stdlib still builds those wrappers on the raw
	// primitives, so it stays exempt via isRuntimeStdPermissionInternal above.
	emit := func(format string, args ...interface{}) {
		a.errorf(pos, "raw concurrency surface removed: "+format, args...)
	}
	switch fnType.Name {
	case "cond_wait":
		emit("`cond_wait` is legacy raw condition-variable surface; use `predicate_wait(cv, move guard, predicate)` or a domain-specific typed predicate wait over protected state")
	case "notify_one", "notify_all":
		emit("`%s` is legacy raw notification surface; use `predicate_notify_one` / `predicate_notify_all` or a domain-specific notifier tied to the protected-state predicate", fnType.Name)
	case "spawn_raw", "join_raw", "detach_raw":
		emit("`%s` is legacy raw thread surface; use `nursery:` / `nursery workers(N):`, a `pool` scope, or a linear typed Thread handle that is joined at a clear boundary", fnType.Name)
	case "spawn1":
		emit("`spawn1` is low-level escaped-task surface; prefer `nursery:` / `nursery workers(N):`, the data-parallel `each(slice, worker)` / `reduce(slice, identity, op)` combinators, or a pool/task-group batch unless the linear Thread handle intentionally escapes")
	case "pool_submit1":
		emit("`pool_submit1` is low-level pool submission surface; prefer `nursery:` for a one-off joined batch, `nursery on(pool):` for joined batches on a reusable caller-owned pool, or the data-parallel `each(slice, worker)` / `reduce(slice, identity, op)` combinators. Use `TaskGroup` when you need a caller-chosen wait point; `wait all` discards results, so keep task handles and `await` them when results matter")
	case "detach":
		emit("`detach` is a detached-task escape hatch; keep work in `nursery:` / pool scope or prove captured state is static/share-safe before using a detached escape")
	}
}

func (a *Analyzer) validateRequiredPermissions(pos lexer.Pos, fnType *FuncType, granted map[string]bool) {
	if fnType == nil {
		return
	}
	mandatoryRefs := a.mandatoryGlobalPermissionRefs(fnType)
	a.errorOnMissingLocalGrant(pos, "call to "+quoteFactTarget(fnType.Name), mandatoryRefs, granted)
	requiredRefs := missingGrantedPermissionRefs(a.permissionRefsRequiringLocalGrant(fnType), a.grantedPermissionRefs(mandatoryRefs))
	missingRefs := missingGrantedPermissionRefs(requiredRefs, granted)
	a.validateLocalPermissionRowGrant(pos, "call to "+quoteFactTarget(fnType.Name), requiredRefs, granted)
	for _, ref := range missingRefs {
		if ref.Name == "Unsafe" && ref.Member == "SegmentMutation" {
			a.errorf(pos, "%s", effectAuthorityGrantMessage("call to "+quoteFactTarget(fnType.Name), []string{"Unsafe"}, "can Unsafe.SegmentMutation"))
			return
		}
		if ref.Name == "Unsafe" && ref.Member == "GuestSegmentInstall" {
			a.errorf(pos, "%s", effectAuthorityGrantMessage("call to "+quoteFactTarget(fnType.Name), []string{"Unsafe"}, "can Unsafe.GuestSegmentInstall"))
			return
		}
		if ref.Name == "Segment" && (ref.Member == "Host" || ref.Member == "Guest") {
			a.errorf(pos, "%s", effectAuthorityGrantMessage("call to "+quoteFactTarget(fnType.Name), []string{"Segment"}, "can Segment."+ref.Member))
			return
		}
	}
	missing := missingGrantedPermissionFamilies(requiredRefs, granted)
	if len(missing) == 0 {
		return
	}
	if a.enforceUnsafePermissions && containsUnsafeFamily(missing) {
		a.errorf(pos, "%s", effectAuthorityGrantMessage("call to "+quoteFactTarget(fnType.Name), missing, permissionGrantHint(requiredRefs, missing)))
		return
	}
	if a.permissionWarningsSuppressedByGenericContext(fnType, granted) {
		return
	}
	a.warnf(pos, effectAuthorityGrantMessage("call to "+quoteFactTarget(fnType.Name), missing, permissionGrantHint(requiredRefs, missing)))
}

// A symbolic row remains a local authority requirement even in a generic body.
// Its declaration in a signature describes the caller contract, not a body grant.
func (a *Analyzer) validateLocalPermissionRowGrant(pos lexer.Pos, label string, refs []ast.PermissionRef, granted map[string]bool) {
	for _, ref := range missingGrantedPermissionRefs(refs, granted) {
		if ref.Member == "" && a.lookupPermissionParam(ref.Name) {
			a.errorOnMissingLocalGrant(pos, label, []ast.PermissionRef{ref}, granted)
		}
	}
}
