package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func (a *Analyzer) analyzeAssignmentStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		if len(a.loopAffineFrames) != 0 {
			if ident, plain := stripOptimizationParens(n.Target).(*ast.Ident); plain {
				if sym, found := a.currentScope.Lookup(ident.Name); found {
					if ref, borrowed := sym.Type.(*RefType); borrowed && returnTypeIsScalarValue(ref.Elem) {
						if a.derivedLoopAliasWrites == nil {
							a.derivedLoopAliasWrites = map[*ast.AssignStmt]bool{}
						}
						a.derivedLoopAliasWrites[n] = true
					}
				}
			}
		}
		// A guest-overlay write `base.field[mem] = value` (docs/107) is desugared to a
		// MemoryManager_WriteU<N> call stashed on n.AsOverlayCall and consumed here, before the
		// normal assignment path would try (and fail) to take the address of the overlay accessor.
		if a.tryDesugarGuestOverlayWrite(n) {
			return
		}
		var targetType Type
		// Analyzing an assignment target reads the base of a field/index path as
		// a location, not a value; don't trip the uninitialized-read check there
		// (the assignment itself initializes it).
		a.suppressUninitReadCheck++
		a.suppressGlobalReadCheck++
		if n.Optional {
			targetType = a.optionalAssignmentTargetType(n.Target)
		} else {
			targetType = a.assignmentTargetType(n.Target)
		}
		a.suppressGlobalReadCheck--
		a.suppressUninitReadCheck--
		isGlobalTarget := false
		if sym, ok := a.globalStorageRoot(n.Target); ok {
			isGlobalTarget = true
			a.recordFunctionPermissionRefs(globalWriteRefs(n.Target.Pos()))
			if a.enforceUnsafePermissions && sym.Kind == SymbolGlobal && sym.Mutable {
				a.recordFunctionPermissionRefs(unsafeMutableGlobalRefs(n.Target.Pos()))
			}
		}
		a.clearPackedVariantViewExpr(n.Target)
		// A store into a global is program-lifetime by definition: when no region is
		// ambient, bind the RHS to the permanent region so container literals and
		// region-polymorphic calls just work (`g_state <- Build_State()`) instead of
		// demanding an explicit region the value would immediately escape anyway.
		// `s <- "..."` into a region-less dstr: rewrite the static string literal to the
		// equivalent byte-list literal before analysis (dstr is darray[u8]; a bare literal is
		// otherwise a static cstr). Parallels the parser's typed-var-decl desugar.
		a.rewriteDStrStringLiteral(&n.Value, targetType)
		savedAllocExpr := a.currentAllocExpr
		restoreAllocExpr := false
		// A synthesized (function-local) ambient auto region must not capture a value
		// stored into a global — it dies at function return. A USER-written region or
		// `in <owner>:` scope is explicit intent and is respected.
		ambientIsSynthesized := a.isCompilerGeneratedAutoRegion(a.activeContainerRegionName())
		if isGlobalTarget && (a.currentAllocExpr == nil || ambientIsSynthesized) &&
			(a.currentFuncType == nil || !a.currentFuncType.RegionPolymorphic) {
			a.currentAllocExpr = &ast.Ident{Position: n.Value.Pos(), Name: "perm"}
			restoreAllocExpr = true
		}
		savedReassignTargets := a.reassignTargets
		if root, ok := rootIdentName(n.Target); ok && root != "" {
			a.reassignTargets = map[string]bool{root: true}
		}
		savedDestCall, savedDestRegion := a.destinationCall, a.destinationRegion
		if !isGlobalTarget {
			if call, isCall := storeScanUnwrap(n.Value).(*ast.CallExpr); isCall && call != nil {
				if region := a.destinationPlaceRegion(n.Target, targetType); region != "" {
					a.destinationCall, a.destinationRegion = call, region
				}
			}
		}
		valueType := a.analyzeValueExpr(n.Value, targetType)
		a.destinationCall, a.destinationRegion = savedDestCall, savedDestRegion
		a.reassignTargets = savedReassignTargets
		a.recordLocalContainerElementAssign(n)
		if restoreAllocExpr {
			a.currentAllocExpr = savedAllocExpr
		}
		// A mutable scalar reference has two intentional assignment forms. If the
		// RHS is a scalar, `<-` writes through the reference; if the RHS is another
		// reference, it rebinds the reference slot. Decide only after RHS analysis
		// so global reference slots do not get mistaken for scalar destinations.
		if ref, ok := a.mutableScalarRefTarget(n.Target); ok && !isBorrowLikeType(valueType) && AssignableTo(ref.Elem, valueType) {
			targetType = ref.Elem
			n.WriteThrough = true
			if ident, ok := stripOptimizationParens(n.Target).(*ast.Ident); ok {
				a.requireWriteThroughTarget(ident, ref)
				// A write through the reference uses its referent exactly as a read does: a
				// store through a ref into storage a push has since moved is a use-after-free.
				a.reportInvalidStorageViewUse(ident)
				if targetSym, ok := a.currentScope.Lookup(ident.Name); ok && a.isInvalidUninitializedSymbol(targetSym) {
					a.errorf(n.Pos(), "cannot write through uninitialized reference %q; initialize or rebind the reference first", ident.Name)
				}
			}
		}
		if !n.Optional && !n.WriteThrough && !a.suppressDiagnostics {
			if refs := a.mutableGlobalBorrowTransferRefs(n.Value, targetType); len(refs) != 0 {
				if a.mutableGlobalBorrowAssignRefs == nil {
					a.mutableGlobalBorrowAssignRefs = make(map[*ast.AssignStmt][]ast.PermissionRef)
				}
				a.mutableGlobalBorrowAssignRefs[n] = refs
			}
		}
		// A store through a byte reference (`r: mutable u8&`, `mutable static u8&`) takes ONE
		// byte. From a byte pointer (a string literal, `cstr`, another `u8&`) that byte is the
		// pointee's first -- never the pointer, which is what a C-string out-parameter means.
		// Codegen used to store the 8-byte pointer instead: past the end of a real byte, and a
		// silent one-byte store once it honours the type. Reject it and name both spellings.
		if _, _, through := a.writeThroughRefTarget(n.Target, targetType); through || n.WriteThrough {
			if isExtendU8Elem(targetType) && isBytePointerValue(n.Value, valueType) {
				a.errorf(n.Pos(), "cannot store a byte pointer (%s) through a byte reference: `<-` stores one byte, not the pointer; declare a C-string out-parameter as `mutable cstr&`, or index the source to copy one byte", valueType)
			}
		}
		// docs/120 §8 single-target arg-manifest `x <- x.method(…)`: a `<-` whose RHS is a
		// void call that mutates x in place (x passed as its receiver/arg). Nothing to
		// assign — the `x <-` is a manifest of what the call mutates. Erase to the
		// already-analyzed call (zero overhead), so `x <- x.push(v)` reads as the
		// reassignment the linear model requires while compiling to the in-place push.
		if id, ok := n.Target.(*ast.Ident); ok &&
			a.isLmutArgManifest([]ast.TupleBindName{{Position: id.Position, Name: id.Name}}, n.Value, valueType, false) {
			n.ArgManifest = true // codegen emits only the call; nothing to assign
			return
		}
		// docs/120 §8 field-path arg-manifest `report.cache <- bump(report.cache)`: the same
		// manifest for a field place of a mutable root; the call writes through the field's address.
		if a.isLmutPlaceArgManifest(n.Target, n.Value, valueType) {
			n.ArgManifest = true
			return
		}
		// docs/120 §8 place-manifest: `place <- place.push(v)` — a mutating builtin whose
		// receiver is the same place as the target. The builtin returns a ref (never void), so
		// this bypasses the void arg-manifest above; erase to the in-place call all the same.
		if a.isPlaceMutatingBuiltinManifest(n.Target, n.Value) {
			n.ArgManifest = true
			return
		}
		if !AssignableTo(targetType, valueType) {
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType, targetType)
			// `x <- 5` on a rebindable read-only reference was meant as a write through it: say why
			// the binding's `mutable` did not make it writable.
			if ref, isRef := targetType.(*RefType); isRef && ref != nil && !ref.Mutable && AssignableTo(ref.Elem, valueType) {
				a.reportRebindableReadOnlyRefNote(n.Pos(), n.Target)
			}
			a.reportShapeMismatchNotes(n.Pos(), targetType, valueType)
		}
		if !n.Optional {
			a.recordAssignmentRefinement(n.Target, targetType, valueType)
			// A store through a reference (`w <- 5`) leaves w bound to the same referent: keep its
			// region and storage-view facts. Recording the scalar as a rebind erased them, so a
			// later push followed by another use of w went unchecked.
			if !n.WriteThrough {
				a.invalidateStorageViewsForWholeAssignment(n.Target, targetType, valueType)
				a.recordRegionRefAssignment(n.Target, n.Value)
				a.recordStorageViewAssignment(n.Target, n.Value)
				a.recordStorageContainerAliasTarget(n.Target, n.Value, targetType, valueType)
			}
		}
		if a.lvalueStorageOutlivesFunction(n.Target) {
			a.checkLocalArenaEscape(n.Value, valueType, "store")
			targetRegion := containerRegion(stripRefForBounds(targetType))
			if targetRegion == "" {
				// A container field reached through a region-carrying struct reference
				// (`map.slots` with `map: StrMap& @r`) lives in that reference's region.
				if field, ok := stripParenExpr(n.Target).(*ast.FieldExpr); ok && field != nil {
					targetRegion = a.fieldObjectExprRegion(field.Object, a.exprType(field.Object))
				}
			}
			if targetRegion == "" {
				targetRegion = a.destinationPlaceRegion(n.Target, targetType)
			}
			a.checkStoredRegionContainerEscapeInto(n.Value, valueType, targetRegion)
		}
		// Nested-region escape: storing an inner-@r value into an outer-region
		// slot dangles once the inner region is freed. Decided by the region
		// outlives-lattice, independent of whether the target outlives the
		// function (the function-outliving case is handled just above).
		diagsBeforeNested := len(a.diagnostics)
		a.checkNestedRegionStoreEscape(n.Target, n.Value, targetType, valueType)
		// An indexed store into a container (`diags[0] <- v`) writes an ELEMENT exactly like
		// `diags.push(v)`; apply the push's element-store rule so a value borrowing local storage
		// cannot be stored into a longer-lived (e.g. parameter) container's region. Skipped when
		// the slot check above already reported, to avoid a duplicate at a second position.
		if len(a.diagnostics) == diagsBeforeNested {
			a.checkIndexedElementStoreEscape(n.Target, n.Value, valueType)
		}
		// A list literal `[v]` stored into a longer-lived container copies an inner-region element's
		// header; the store-site check above only sees the literal's own (target) region, so the
		// element regions are checked separately.
		a.checkFreshContainerStoreEscape(n.Target, targetType, n.Value)
		// Struct copy-by-value can launder a dangling interior region-container
		// field (whose region is not on the struct type) past the region's death;
		// the interior-taint side-table makes that region visible to the check.
		a.checkStructCopyInteriorRegionEscape(n.Target, targetType, n.Value, valueType)
		a.recordStructInteriorRegionTaint(n.Target, n.Value, valueType)
		// Reassigning a struct local invalidates any region recorded at its declaration; re-record
		// from the new RHS or clear, so a later call never threads a stale (possibly dead) region.
		a.invalidateStructLocalAllocRegionOnAssign(n.Target, n.Value, valueType)
		a.narrowStructLocalHomeRegionOnWrite(n.Target)
		a.checkStoredBorrowEscapesLocal(n.Target, targetType, n.Value, valueType)
		if ident, ok := n.Target.(*ast.Ident); ok && a.currentScope != nil {
			if targetSym, ok := a.currentScope.Lookup(ident.Name); ok {
				if from, fromType, ok := a.freezeMovedPackedStoreSource(n.Value); ok {
					a.remapPackedStoreDependencies(from, targetSym, PackedEnumStoreWithState(fromType, a.namedTypes["Frozen"]))
				}
			}
		}
		// Re-discharge the where-predicate on a struct field store (recv.field <- value). The field's
		// declared type may be `T where P`; storing a new value into the field must re-prove P just as
		// the construction site does. This is a no-op for non-struct targets or fields without a where
		// clause. Mirrors the local-variable reassignment check just below (whereTypeForReassignTarget).
		if fe, ok := n.Target.(*ast.FieldExpr); ok {
			a.dischargeFieldStoreWhere(fe.Object, fe.Field, n.Value, n.Pos())
		}
		a.recordSpecializedValueTypeTarget(n.Target, valueType)
		if !n.Optional {
			a.recordNamedStateAssignmentTarget(n.Target, n.Value, valueType)
			a.invalidatePredFactsForTarget(n.Target)
			a.invalidateRangeFactsForTarget(n.Target)
			a.checkFrameWrite(n.Target)
			a.recordWrittenConstForTarget(n.Target, n.Value)
			a.recordWrittenFieldForTarget(n.Target, n.Value)
			a.recordConstAssignmentRangeFact(n.Target, n.Value)
			a.recordCastRangeFact(n.Target, n.Value)
			if !n.WriteThrough {
				a.clearInvalidUninitializedForExpr(n.Target)
			}
			a.rejectAffineIndexOverwrite(n.Target)
			a.clearAffineValueTarget(n.Target)
			a.trackAffineValueTarget(n.Target, targetType)
			a.markCreatedProtocolTarget(n.Target, n.Value, targetType)
			a.recordBorrowedOwnerRefTarget(n.Target, targetType, n.Value)
			a.recordFunctionValueTarget(n.Target, n.Value)
			a.recordLocalRefAliasAssignment(n, targetType)
			a.recordStructAliasCarrierFieldAssignment(n.Target, targetType, n.Value)
		}
		if AssignableTo(targetType, valueType) {
			a.bindActivePackedStoreType(targetType)
		}
		a.noteGlobalStorageAliasAssignment(n.Target, n.Value, targetType)
		a.consumeAffineValueExpr(n.Value, targetType, "assignment")
		a.invalidateIndexBoundsForAssignedTarget(n.Target)
		a.invalidateSMTAssertFactsForTarget(n.Target)
		if !n.Optional {
			a.recordSMTAssignmentFact(n.Target, n.Value)
			// Re-discharge the where-invariant on reassignment of a where-typed local. The binding's
			// declared type still advertises `x where P`, so the new RHS must re-prove P[x ↦ rhs] —
			// otherwise an out-of-range reassignment silently violates the refinement. No-op unless the
			// target is a where-typed local (whereTypeForReassignTarget gates this). Mirrors the
			// binding-site cycle in dischargeLocalWhereRefinement: refuted → hard error, unknown →
			// runtime lint, proved → re-seed the fact for the new value.
			if wt, name, ok := a.whereTypeForReassignTarget(n.Target); ok {
				if !a.dischargeWhereRefinement(wt, name, n.Value, n.Pos(), "where refinement on reassignment") {
					a.recordProof(n.Pos(), "where refinement on reassignment", "where", ProofRuntime)
					subst := map[string]ast.Expr{}
					if n.Value != nil {
						subst[name] = n.Value
					}
					diag := a.buildRequiresFailureDiagnostic(wt.Predicate, subst, "")
					a.proofLint(n.Pos(), "%s", diag.Format("where refinement on reassignment"))
				}
				a.seedRangeFactsFromCondition(wt.Predicate)
			}
		}
	case *ast.AugAssignStmt:
		targetType := a.assignmentTargetType(n.Target)
		if sym, ok := a.globalStorageRoot(n.Target); ok {
			a.recordFunctionPermissionRefs(globalReadRefs(n.Target.Pos()))
			a.recordFunctionPermissionRefs(globalWriteRefs(n.Target.Pos()))
			if a.enforceUnsafePermissions && sym.Kind == SymbolGlobal && sym.Mutable {
				a.recordFunctionPermissionRefs(unsafeMutableGlobalRefs(n.Target.Pos()))
			}
		}
		if array, _, ok := builtinDArrayPushReceiverType(targetType); ok && n.Op == lexer.TOKEN_PLUSEQ {
			if n.CollectionAppend == nil {
				n.CollectionAppend = collectionAppendCall(n)
			}
			a.analyzeExpr(n.CollectionAppend)
			valueType := a.exprType(n.Value)
			if !AssignableTo(array.Elem, valueType) {
				a.errorf(n.Pos(), "darray += requires one element of type %s, got %s; use extend for multiple elements", array.Elem, valueType)
			}
		} else {
			n.CollectionAppend = nil
			valueType := a.analyzeExpr(n.Value)
			if !IsNumericType(targetType) || !IsNumericType(valueType) {
				a.errorf(n.Pos(), "augmented assignment requires numeric operands")
			}
		}
		a.recordNamedStateAugAssignTarget(n.Target)
		a.invalidatePredFactsForTarget(n.Target)
		a.invalidateRangeFactsForTarget(n.Target)
		a.checkFrameWrite(n.Target)
		a.invalidateWrittenConst(rootIdentNameOrEmpty(n.Target))
		a.invalidateWrittenConstForLaunderedRoots(n.Target)
		// An augmented field write (`self.f += x`) changes that field to a non-tracked value: drop just
		// its field fact (siblings untouched), and drop the root's fields wholesale for any other shape.
		a.invalidateWrittenFieldForWrite(n.Target)
		a.invalidateIndexBoundsForAssignedTarget(n.Target)
		a.invalidateSMTAssertFactsForTarget(n.Target)
	case *ast.AsRefAssignStmt:
		a.suppressUninitReadCheck++
		a.suppressGlobalReadCheck++
		targetType := a.asRefTargetType(n.Target, n.AsKind)
		a.suppressGlobalReadCheck--
		a.suppressUninitReadCheck--
		if sym, ok := a.globalStorageRoot(n.Target); ok {
			a.recordFunctionPermissionRefs(globalWriteRefs(n.Target.Pos()))
			if a.enforceUnsafePermissions && sym.Kind == SymbolGlobal && sym.Mutable {
				a.recordFunctionPermissionRefs(unsafeMutableGlobalRefs(n.Target.Pos()))
			}
		}
		a.clearPackedVariantViewExpr(n.Target)
		valueType := a.analyzeValueExpr(n.Value, targetType)
		if !AssignableTo(targetType, valueType) {
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType, targetType)
			a.reportShapeMismatchNotes(n.Pos(), targetType, valueType)
		}
		a.recordAssignmentRefinement(n.Target, targetType, targetType)
		a.recordRegionRefAssignment(n.Target, n.Value)
		a.recordStorageViewAssignment(n.Target, n.Value)
		a.recordStorageContainerAliasTarget(n.Target, n.Value, targetType, valueType)
		a.recordSpecializedValueTypeTarget(n.Target, valueType)
		a.recordNamedStateAssignmentTarget(n.Target, n.Value, valueType)
		a.invalidatePredFactsForTarget(n.Target)
		a.invalidateRangeFactsForTarget(n.Target)
		a.checkFrameWrite(n.Target)
		a.recordWrittenConstForTarget(n.Target, n.Value)
		// An `as &`/`as !` store rebinds a reference place, not a plain value; do not record it as a
		// stable field value — just drop the affected field fact so nothing stale survives.
		a.invalidateWrittenFieldForWrite(n.Target)
		a.recordConstAssignmentRangeFact(n.Target, n.Value)
		a.recordCastRangeFact(n.Target, n.Value)
		a.clearInvalidUninitializedForExpr(n.Target)
		a.clearAffineValueTarget(n.Target)
		a.trackAffineValueTarget(n.Target, targetType)
		a.markCreatedProtocolTarget(n.Target, n.Value, targetType)
		a.recordBorrowedOwnerRefTarget(n.Target, targetType, n.Value)
		a.recordFunctionValueTarget(n.Target, n.Value)
		if AssignableTo(targetType, valueType) {
			a.bindActivePackedStoreType(targetType)
		}
		a.noteGlobalStorageAliasAssignment(n.Target, n.Value, targetType)
		a.consumeAffineValueExpr(n.Value, targetType, "assignment")
		a.invalidateSMTAssertFactsForTarget(n.Target)
		a.recordSMTAssignmentFact(n.Target, n.Value)
	}
}
