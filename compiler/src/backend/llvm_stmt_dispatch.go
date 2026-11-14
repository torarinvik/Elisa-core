//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"fmt"
)

func (s *functionState) emitStmtInner(stmt ast.Stmt) error {
	if s.g.di != nil {
		s.g.di.setLoc(s, stmt)
	}
	if s.g.trace != nil {
		s.g.trace.recordStmt(s, stmt)
	}
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		return s.emitLocalVarDecl(n)
	case *ast.LetDestructureStmt:
		return s.emitLetDestructureStmt(n)
	case *ast.TupleBindStmt:
		if n.ArgManifest {
			// docs/120 §8: the names are a manifest of what a void call mutates in place —
			// emit only the call (its refs do the mutation), discard the (void) result.
			_, _, err := s.emitExpr(n.Value, nil)
			return err
		}
		return s.emitTupleBindStmt(n)
	case *ast.MoveBindStmt:
		return s.emitMoveBindStmt(n)
	case *ast.DeferStmt:
		return s.emitDeferStmt(n)
	case *ast.ScopeStmt:
		return s.emitScopeStmt(n)
	case *ast.RegionStmt:
		if len(n.Body) != 0 || n.OwnerName != "" {
			return s.emitScopedArenaStmt(n)
		}
		return s.emitRegionDecl(n)
	case *ast.MarkStmt:
		regionBinding, ok := s.lookupBinding(n.RegionName)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.RegionName)
		}
		markType := s.g.namedType("ArenaMark")
		if markType == nil {
			return fmt.Errorf("missing builtin ArenaMark type for region checkpoints")
		}
		alloca, err := s.createEntryAlloca(n.Name, markType)
		if err != nil {
			return err
		}
		markValue, err := s.emitArenaSnapshot(regionBinding.ptr, regionBinding.typ)
		if err != nil {
			return err
		}
		llvmBuildStore(s.builder, markValue, alloca)
		s.defineBinding(n.Name, valueBinding{ptr: alloca, typ: markType})
		return nil
	case *ast.CheckpointStmt:
		return s.emitCheckpointStmt(n)
	case *ast.GroupedCheckpointStmt:
		return s.emitGroupedCheckpointStmt(n)
	case *ast.RestoreStmt:
		regionBinding, ok := s.lookupBinding(n.RegionName)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.RegionName)
		}
		markBinding, ok := s.lookupBinding(n.MarkName)
		if !ok {
			return fmt.Errorf("unknown checkpoint %q during LLVM lowering", n.MarkName)
		}
		markValue, err := s.loadValue(markBinding.ptr, markBinding.typ, n.MarkName)
		if err != nil {
			return err
		}
		return s.emitArenaRewind(regionBinding.ptr, regionBinding.typ, markValue, markBinding.typ)
	case *ast.RestoreCheckpointStmt:
		return s.emitRestoreCheckpointStmt(n)
	case *ast.ResetStmt:
		regionBinding, ok := s.lookupBinding(n.Name)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.Name)
		}
		return s.emitArenaReset(regionBinding.ptr, regionBinding.typ)
	case *ast.DestroyStmt:
		binding, ok := s.lookupBinding(n.Name)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.Name)
		}
		return s.emitArenaFree(binding.ptr, binding.typ)
	case *ast.AdoptStmt:
		childBinding, ok := s.lookupBinding(n.Child)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.Child)
		}
		parentBinding, ok := s.lookupBinding(n.Parent)
		if !ok {
			return fmt.Errorf("unknown region %q during LLVM lowering", n.Parent)
		}
		return s.emitArenaAdopt(parentBinding.ptr, childBinding.ptr, parentBinding.typ)
	case *ast.PromoteStmt:
		return s.emitPromoteStmt(n)
	case *ast.AssignStmt:
		if n.ArgManifest {
			// docs/120 §8: `x <- x.method(…)` — the target manifests what a void call mutates
			// in place; emit only the call, discard the (void) result.
			_, _, err := s.emitExpr(n.Value, nil)
			return err
		}
		if n.AsOverlayCall != nil {
			// Guest-overlay write (docs/107): `base.field[mem] = value` was desugared by the analyzer
			// to a MemoryManager_WriteU<N> call. Emit that call in place of the store; the lowering is
			// byte-identical to the hand-written write.
			if s.g.trace != nil {
				s.g.trace.recordStep(s, n.Pos().Line)
			}
			_, _, err := s.emitCallExpr(n.AsOverlayCall)
			return err
		}
		if n.FastMath {
			// A comprehension fold's accumulator update: emit its value with reassociation+contraction
			// so the reduction re-brackets into the vectorizable tree form (the defined reduction order
			// for a fold, docs/79), scoped to this statement. The defer pops when emitStmt returns, i.e.
			// right after this assignment is emitted. Integer accumulators are unaffected by FP flags.
			s.reduceReassocScope++
			defer func() { s.reduceReassocScope-- }()
		}
		if n.Optional {
			if s.g.trace != nil {
				s.g.trace.recordStep(s, n.Pos().Line)
			}
			return s.emitOptionalAssignStmt(n)
		}
		if n.WriteThrough {
			// `x <- v` on a mutable scalar-reference binding: the analyzer typed the store as the
			// REFERENT, so load the reference from x's slot and store v through it. The slot is
			// not the destination (that path coerced v to a pointer and rebound x to it).
			slotPtr, slotType, err := s.emitAddress(n.Target)
			if err != nil {
				return err
			}
			refType, ok := slotType.(*semantic.RefType)
			if !ok || refType == nil {
				return fmt.Errorf("write-through assignment at source line %d: target is %v, not a reference binding", n.Pos().Line, slotType)
			}
			refPtr, err := s.loadValue(slotPtr, refType, "ref.slot")
			if err != nil {
				return err
			}
			value, _, err := s.emitExpr(n.Value, refType.Elem)
			if err != nil {
				return err
			}
			if err := s.storeValue(refPtr, value, refType.Elem, "ref.assign"); err != nil {
				return err
			}
			if s.g.trace != nil {
				assignName := ""
				if id, ok := n.Target.(*ast.Ident); ok {
					assignName = id.Name
				}
				s.g.trace.recordValue(s, n.Pos().Line, assignName, value, refType.Elem)
			}
			s.invalidatePackedReadCaches()
			return nil
		}
		if identTarget, ok := n.Target.(*ast.Ident); ok {
			if binding, ok := s.lookupBinding(identTarget.Name); ok && !binding.mutable {
				if refType, ok := binding.typ.(*semantic.RefType); ok {
					slotPtr, err := s.loadValue(binding.ptr, binding.typ, identTarget.Name+".ref.slot")
					if err != nil {
						return err
					}
					// The analyzer types a store through an immutable reference binding as the
					// REFERENT, auto-reading a reference RHS (`r <- s` copies *s). Storing the
					// binding's own (pointer) type instead wrote s's ADDRESS into *r: a wrong value,
					// and an 8-byte store past the end of a `u8`/`i32` referent.
					storeType := refType.Elem
					value, _, err := s.emitExpr(n.Value, storeType)
					if err != nil {
						return err
					}
					if err := s.storeValue(slotPtr, value, storeType, identTarget.Name+".ref.assign"); err != nil {
						return fmt.Errorf("while emitting %s ref assignment at source line %d target %q (rhs %T has semantic type %v): %w", s.decl.Name, n.Pos().Line, identTarget.Name, n.Value, s.exprType(n.Value), err)
					}
					if s.g.trace != nil {
						s.g.trace.recordValue(s, n.Pos().Line, identTarget.Name, value, storeType)
					}
					s.invalidatePackedReadCaches()
					return nil
				}
			}
		}
		if fieldTarget, ok := n.Target.(*ast.FieldExpr); ok {
			if handled, err := s.emitBitGroupMemberAssign(fieldTarget, n.Value); handled {
				if err != nil {
					return err
				}
				if s.g.trace != nil {
					s.g.trace.recordStep(s, n.Pos().Line)
				}
				s.invalidatePackedReadCaches()
				return nil
			}
		}
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		s.invalidatePackedEnumStorageExpr(n.Target)
		s.invalidatePackedEnumStoreOriginExpr(n.Target)
		s.invalidatePackedCommonFieldValuesExpr(n.Target)
		s.invalidatePackedVariantViewExpr(n.Target)
		// Reassigning a seeded container into a stack-tagged darray (`xs <- [..]`) must allocate
		// the new backing into that darray's parallel arena, exactly like the VarDecl seed path —
		// otherwise a later grower's realloc straddles the region base arena (task_00a7fdf3).
		savedAssignSink := s.currentDArraySinkTag
		if id, ok := n.Target.(*ast.Ident); ok && s.darrayStackTag != nil {
			if tag, ok := s.darrayStackTag[id.Name]; ok {
				s.currentDArraySinkTag = tag
			}
		}
		value, _, err := s.emitExpr(n.Value, targetType)
		s.currentDArraySinkTag = savedAssignSink
		if err != nil {
			return err
		}
		value, err = s.emitGlobalRegionHandoff(n.Target, ptr, value, targetType)
		if err != nil {
			return err
		}
		if s.g.result != nil && s.g.semanticReadView().ValueReassignReclaims(n) {
			if err := s.emitReassignReclaim(n, ptr, targetType); err != nil {
				return err
			}
		}
		if err := s.storeValue(ptr, value, targetType, "assign"); err != nil {
			return err
		}
		// Struct field invariants (debug builds): after `obj.field <- v`, re-check obj's invariants.
		if s.g.optLevel == OptimizationLevel0 || s.g.forceContracts {
			if fieldTarget, ok := n.Target.(*ast.FieldExpr); ok && fieldTarget.Object != nil {
				if st := backendStructTypeOf(s.exprType(fieldTarget.Object)); st != nil && st.Decl != nil && len(st.Decl.Invariants) > 0 {
					objPtr, _, addrErr := s.emitAddress(fieldTarget.Object)
					if addrErr != nil {
						return addrErr
					}
					if err := s.emitStructInvariantChecksAt(objPtr, st); err != nil {
						return err
					}
				}
			}
		}
		if s.g.trace != nil {
			assignName := ""
			if id, ok := n.Target.(*ast.Ident); ok {
				assignName = id.Name
			}
			// Value record for a scalar ident; otherwise (field/index target, or a
			// non-scalar value) recordValue falls back to a plain step.
			s.g.trace.recordValue(s, n.Pos().Line, assignName, value, targetType)
		}
		s.bindPackedStoreValue(targetType, value)
		if path, ok := s.packedEnumStoragePath(n.Target); ok {
			if err := s.bindPackedStoreOriginsForExprPath(path, n.Value, targetType); err != nil {
				return err
			}
		}
		s.invalidatePackedReadCaches()
		return nil
	case *ast.AsRefAssignStmt:
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		s.invalidatePackedEnumStorageExpr(n.Target)
		s.invalidatePackedEnumStoreOriginExpr(n.Target)
		s.invalidatePackedCommonFieldValuesExpr(n.Target)
		s.invalidatePackedVariantViewExpr(n.Target)
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		if err := s.storeValue(ptr, value, targetType, "asref.assign"); err != nil {
			return err
		}
		s.bindPackedStoreValue(targetType, value)
		if path, ok := s.packedEnumStoragePath(n.Target); ok {
			if err := s.bindPackedStoreOriginsForExprPath(path, n.Value, targetType); err != nil {
				return err
			}
		}
		s.invalidatePackedReadCaches()
		return nil
	case *ast.AugAssignStmt:
		if n.CollectionAppend != nil {
			_, _, err := s.emitExpr(n.CollectionAppend, nil)
			return err
		}
		ptr, targetType, err := s.emitAddress(n.Target)
		if err != nil {
			return err
		}
		current, err := s.loadValue(ptr, targetType, "aug.cur")
		if err != nil {
			return err
		}
		value, _, err := s.emitExpr(n.Value, targetType)
		if err != nil {
			return err
		}
		result, err := s.emitAugmentedValue(n.Op, current, value, targetType)
		if err != nil {
			return err
		}
		llvmBuildStore(s.builder, result, ptr)
		s.invalidatePackedCommonFieldValuesExpr(n.Target)
		s.invalidatePackedReadCaches()
		return nil
	case *ast.ReturnStmt:
		if n.Value == nil {
			// docs/90: enforce value-contract `ensure <bool>` postconditions (over params / `old(...)`)
			// on a void return too — the non-void path runs these via emitFunctionReturn, but a bare
			// `return` lowers to RetVoid directly and would otherwise skip them. `result` is void here,
			// so emitPostconditionChecks binds nothing and just emits the boolean checks.
			if err := s.emitPostconditionChecks(nil, nil); err != nil {
				return err
			}
			// docs/85 brick 2 B: enforce `ensures <param> is Law` postconditions on a void return too.
			if err := s.emitRefinementPostconditionChecks(); err != nil {
				return err
			}
			if err := s.emitActiveScopedCleanup(); err != nil {
				return err
			}
			if s.currentBlockTerminated() {
				return nil
			}
			if err := s.emitRegionCleanup(); err != nil {
				return err
			}
			if retUnion, ok := s.fnType.Return.(*semantic.ErrorUnionType); ok && isVoidType(retUnion.Value) {
				zeroCode, err := s.errorCodeConstant(0)
				if err != nil {
					return err
				}
				// A payloaded error set lowers the void union to a {code, payloads...}
				// struct; wrap the success code into it so the value matches the type.
				successValue, err := s.wrapVoidErrorUnionCode(retUnion.Errors, zeroCode)
				if err != nil {
					return err
				}
				llvmBuildRet(s.builder, successValue)
				return nil
			}
			if s.mainReturnsStatus {
				zero := llvmConstInt(llvmInt32TypeInContext(s.g.context), 0, 0)
				llvmBuildRet(s.builder, zero)
			} else {
				llvmBuildRetVoid(s.builder)
			}
			return nil
		}
		// When the function returns a reference (`T&`), pass that as the expected type so
		// a value-typed lvalue (e.g. `return s[h]`) is materialized as the element's
		// address rather than reinterpreted as a pointer. Other return types keep the
		// untyped (nil) path so emitFunctionReturn drives coercion.
		var returnExpected semantic.Type
		if _, ok := s.fnType.Return.(*semantic.RefType); ok {
			returnExpected = s.fnType.Return
		}
		// A tuple literal returned from a tuple-typed function takes the declared field
		// types: a `get … else return (null, x)` recovery is analyzed outside the return
		// context, so its recorded type has a bare-null field that would lower to `ptr`
		// instead of the declared Optional and fail the LLVM return-type check.
		if _, isTupleLit := n.Value.(*ast.TupleExpr); isTupleLit {
			if _, ok := semantic.StripAggregateStateType(s.fnType.Return).(*semantic.TupleType); ok {
				returnExpected = s.fnType.Return
			}
		}
		if err := s.emitReturnRefinementChecks(n); err != nil {
			return err
		}
		value, valueType, err := s.emitExpr(n.Value, returnExpected)
		if err != nil {
			return err
		}
		return s.emitFunctionReturn(value, valueType)
	case *ast.BreakStmt:
		if len(s.breakTargets) == 0 {
			return fmt.Errorf("break outside loop during LLVM lowering")
		}
		if err := s.emitLoopExitCleanup(); err != nil {
			return err
		}
		if !s.currentBlockTerminated() {
			llvmBuildBr(s.builder, s.breakTargets[len(s.breakTargets)-1])
		}
		return nil
	case *ast.ContinueStmt:
		if len(s.continueTargets) == 0 {
			return fmt.Errorf("continue outside loop during LLVM lowering")
		}
		if err := s.emitLoopExitCleanup(); err != nil {
			return err
		}
		if !s.currentBlockTerminated() {
			llvmBuildBr(s.builder, s.continueTargets[len(s.continueTargets)-1])
		}
		return nil
	case *ast.IfStmt:
		return s.emitIf(n)
	case *ast.MatchStmt:
		return s.emitMatch(n)
	case *ast.ExpectPatternStmt:
		return s.emitExpectPatternStmt(n)
	case *ast.InStoreStmt:
		return s.emitInStore(n)
	case *ast.CanStmt:
		// A `can Scalar` grant suppresses expected-to-vectorize loop tagging for the block it
		// lexically covers (see functionState.scalarGrantDepth / tagAutovecExpectedLoop).
		if permissionRefsGrantScalar(n.Permissions) {
			s.scalarGrantDepth++
			err := s.emitBlock(n.Body, true)
			s.scalarGrantDepth--
			return err
		}
		return s.emitBlock(n.Body, true)
	case *ast.PoolStmt:
		return s.emitPoolStmt(n)
	case *ast.LockStmt:
		return s.emitLockStmt(n)
	case *ast.WhileStmt:
		return s.emitWhile(n)
	case *ast.ForStmt:
		return s.emitForStmt(n)
	case *ast.IterForStmt:
		return s.emitIterForStmt(n)
	case *ast.ParallelForStmt:
		return s.emitParallelForStmt(n)
	case *ast.PassStmt:
		return nil
	case *ast.SignalStmt:
		return nil
	case *ast.PanicStmt:
		return s.emitPanicWithBacktrace(n.Pos(), n.Message)
	case *ast.ExprStmt:
		if call, ok := n.Expr.(*ast.CallExpr); ok && s.g != nil && s.g.result != nil && s.g.semanticReadView().ValueLemmaCalls(call) {
			// A lemma call is ghost code: verification already consumed it (requires discharged,
			// ensures injected as facts). Emit nothing.
			return nil
		}
		if s.g != nil && s.g.result != nil && s.g.semanticReadView().ValueGhostContracts(n.Expr) {
			// An `assert` that reads a ghost var is verification-only — erase its runtime check.
			return nil
		}
		if cond, ok := backendAssertedCondition(s.g.result, n.Expr); ok {
			// A plain `assert(COND)` is NOT a call to a function named `assert` — the analyzer already
			// claimed it (analyzer_flow.go: discharged through the proof ladder, COND recorded as a
			// downstream flow fact). Lower it exactly like `assert … by:`: a debug-gated runtime check,
			// erased at higher opt levels. Emitting it as an ordinary call would fail to resolve `assert`.
			if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
				return nil
			}
			return s.emitContractCheck(cond, "assertion failed")
		}
		_, _, err := s.emitExpr(n.Expr, nil)
		return err
	case *ast.DiscardStmt:
		_, _, err := s.emitExpr(n.Value, nil)
		return err
	case *ast.StaticIfStmt:
		return s.emitStaticIf(n)
	case *ast.StaticErrorStmt:
		return fmt.Errorf("static error should not reach LLVM lowering")
	case *ast.ContractStmt:
		return s.emitInBodyInvariant(n, true)
	case *ast.AssertByStmt:
		// The `by:` proof block is verification-only (already consumed by the analyzer) and is ERASED:
		// nothing from it is lowered. COND itself was statically proven; emit it as a debug-gated
		// assertion check, consistent with an ordinary assert (and a no-op at higher opt levels).
		if n.Cond == nil {
			return nil
		}
		if s.g.optLevel != OptimizationLevel0 && !s.g.forceContracts {
			return nil
		}
		return s.emitContractCheck(n.Cond, "assertion failed")
	case *ast.ProofBlockStmt:
		// Standalone proofs are verification-only closed-world regions. The analyzer consumes the
		// proof and exports only the goal as a flow fact; no runtime check or proof body is emitted.
		return nil
	case *ast.AssertHoleStmt:
		return nil
	case *ast.StaticAssertStmt:
		return s.emitStaticAssert(n)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			if err := s.emitStaticAssert(&ast.StaticAssertStmt{Position: item.Position, Cond: item.Cond, Message: item.Message}); err != nil {
				return err
			}
		}
		return nil
	case *ast.StaticBlockStmt:
		return s.emitStaticBlock(n.Body)
	case *ast.MachineCoverageStmt:
		// Compile-time-only: the machine's input coverage was verified in analysis (docs/125
		// §9.1). The real dispatch is the desugared if-ladder; this carries no runtime effect.
		return nil
	default:
		return fmt.Errorf("unsupported statement %T", stmt)
	}
}
