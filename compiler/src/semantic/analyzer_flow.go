package semantic

import (
	"elisacore/src/ast"
	"fmt"
)

func (a *Analyzer) analyzeStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		// Expand a named refinement alias used in this local's binder (`x: Positive = ...`) into the
		// equivalent anonymous `Base where Pred` in place, before its type is resolved, so the existing
		// local where discharge handles it with no parallel path.
		a.rewriteLocalRefineAlias(n)
		if n.Ghost {
			// A ghost decl is verification-only: record it for codegen erasure, and analyze its
			// initializer in a ghost-read-allowed context (a ghost may be defined in terms of another
			// ghost). The bound symbol is flagged Ghost so any later READ outside a contract is rejected.
			if a.ghostDecls == nil {
				a.ghostDecls = map[*ast.VarDeclStmt]bool{}
			}
			a.ghostDecls[n] = true
			a.ghostReadAllowed++
			defer func() { a.ghostReadAllowed-- }()
			// SOUNDNESS of erasure: a ghost initializer is dropped from codegen, so it must have no
			// observable runtime effect. Restrict it to a side-effect-free expression grammar (reads,
			// arithmetic, field/index access, `old(...)`). A general call could mutate real state, which
			// erasing would silently change — reject it.
			if n.Value != nil && !ghostInitIsErasureSafe(n.Value) {
				a.errorf(n.Value.Pos(), "ghost variable %q initializer must be side-effect-free (it is erased from codegen): use reads, arithmetic, field/index access, or `old(...)`, not a general call or mutation", n.Name)
			}
		}
		var declType Type
		var valueType Type
		if n.Type != nil {
			declType = a.resolveType(n.Type)
		}
		if n.Value != nil {
			if n.Type == nil && a.ambiguousDArrayBuilders != nil && a.ambiguousDArrayBuilders[n] {
				a.errorf(n.Pos(), "cannot infer element type for empty darray builder %q; add a type declaration (for example `%s: T[] = []` or `%s: mutable darray[T] = []`) or push/extend typed values before type-dependent darray operations", n.Name, n.Name, n.Name)
				declType = invalidType
				valueType = invalidType
				a.exprTypes[n.Value] = invalidType
			} else {
				valueType = a.analyzeValueExpr(n.Value, declType)
			}
			if declType == nil {
				declType = valueType
			} else if !AssignableTo(declType, valueType) {
				a.errorf(n.Pos(), "variable %q expects %s, got %s", n.Name, declType, valueType)
				a.reportShapeMismatchNotes(n.Pos(), declType, valueType)
			}
			// Nested-region escape: binding an inner-@r value into a variable whose
			// declared type names an outer region dangles once the inner region is
			// freed (the var-decl analogue of the assignment/push check). Decided by
			// the region outlives-lattice.
			a.checkNestedRegionStoreEscape(n.Value, declType, valueType)
			a.checkFreshContainerStoreEscape(&ast.Ident{Position: n.Position, Name: n.Name}, declType, n.Value)
		} else if declType == nil {
			a.errorf(n.Pos(), "variable %q requires a type or initializer", n.Name)
			declType = invalidType
		}
		bindingType := declType
		if dstRef, ok := bindingType.(*RefType); ok {
			if srcRef, ok := valueType.(*RefType); ok && srcRef.Mutable && !dstRef.Mutable && AssignableTo(bindingType, valueType) {
				cloned := cloneRefType(dstRef)
				cloned.Mutable = true
				bindingType = cloned
			}
		}
		if specializedViewType, ok := concreteDArrayViewBindingType(bindingType, valueType); ok {
			bindingType = specializedViewType
		}
		if !n.Mutable {
			if specializedType, ok := a.specializeCallbackCarryingType(bindingType, valueType); ok {
				bindingType = specializedType
			}
		}
		// Region-parameterized containers: a container local declared inside an
		// `in <region>:` scope carries that region in its type; escape checks and
		// codegen arena routing consult it (see REGION_CONTAINERS_DESIGN.md).
		bindingType = a.stampContainerRegion(bindingType)
		sym := &Symbol{Name: n.Name, Kind: SymbolLocal, Type: bindingType, Node: n, Mutable: n.Mutable, Ghost: n.Ghost}
		a.defineLocal(sym, n.Pos())
		a.recordRefinementChecks(n)
		a.seedWhereRefinementFact(n)
		if n.Value != nil && isZeroedInitializer(n.Value) {
			a.markZeroedUninitialized(sym)
		}
		if n.Mutable && n.Value != nil {
			a.recordSMTAssignmentFact(&ast.Ident{Position: n.Position, Name: n.Name}, n.Value)
			a.recordConstAssignmentRangeFact(&ast.Ident{Position: n.Position, Name: n.Name}, n.Value)
		}
		a.recordSpecializedValueTypeBinding(sym, valueType)
		// An immutable local with a compile-time-constant initializer (`k: i32 = 5`) is pinned to
		// that value for its whole lifetime — record it as a written-constant so the interval prover
		// (boundAffine) and refinement discharge can treat it as an exact value. Sound: immutable, so
		// never invalidated. recordWrittenConstForTarget no-ops for a non-const RHS.
		if !n.Mutable && n.Value != nil {
			a.recordWrittenConstForTarget(&ast.Ident{Position: n.Position, Name: n.Name}, n.Value)
			// An immutable integer binding to a length-ish expression (`n = xs.count`) makes `n`
			// provably equal to that expression for the binding's lifetime — a cross-variable bound
			// equality (docs/86 brick 86-6) that lets `for i in 0..<n: xs[i]` discharge. Invalidated if
			// the referenced container is mutated (shared with the index-bound invalidation sites).
			if IsNumericType(bindingType) && !IsFloatType(bindingType) {
				a.recordBoundEqual(n.Name, optimizationExprString(n.Value))
			}
			// docs/90 brick 90-7: when the initializer is a direct call to a function with a refined
			// return type (`-> i64 is Bounded[..]`), assume that postcondition as a flow fact on the
			// binding. The callee proves its return refinement; the caller now uses it without
			// re-deriving it from the body (modular verification).
			a.seedReturnRefinementFacts(n.Name, n.Value, bindingType)
			// docs P1 (protocol-contract composition): when the initializer is a generic call to a
			// protocol method through a bounded type param, assume the PROTOCOL's `ensure` (with `result`
			// bound to this immutable binding) as a flow fact — the caller half of behavioral subtyping.
			// Sound for an immutable binding: the fact is never invalidated by a later reassignment.
			a.seedProtocolEnsureFacts(n.Name, n.Value)
		}
		a.recordValueBinding(sym, n.Value)
		a.recordViewStaticLenBinding(n.Name, n.Value, bindingType)
		a.markCreatedProtocolSymbol(sym, n.Value)
		a.markReceivedOwnedRegion(sym, n.Value)
		if isOwnedTypeExpr(n.Type) {
			// An owned region is move-only: rebinding it to another `owned`
			// binding must transfer it (consume the source), never copy the
			// allocator handle into a second owner (which would double-free).
			if srcOwner, ok := a.returnedRegionOwner(n.Value); ok {
				a.transferRegionOwnerOut(srcOwner, "moved into binding")
			} else if srcOwner, ok := a.regionOwnerIdent(n.Value); ok {
				a.errorf(n.Value.Pos(), "owned region %q is move-only: use `move %s` to transfer ownership to %q", srcOwner.Name, srcOwner.Name, n.Name)
			}
			a.registerOwnedStoreOwner(sym)
		}
		a.recordBorrowedOwnerRefBinding(sym, n.Value)
		a.recordFunctionValueBinding(sym, n.Value)
		a.recordImmutableSymbolOptimizationFacts(sym, n.Value)
		a.recordRegionRefBinding(sym, n.Value)
		// Propagate interior-region taint through `let dest = src` so a copied
		// region-less aggregate carries its dangling interior field forward.
		a.recordStructInteriorRegionTaint(&ast.Ident{Position: n.Position, Name: n.Name}, n.Value, valueType)
		a.recordStorageViewBinding(sym, n.Value)
		a.dischargeLocalWhereRefinement(n)
		aliasAccessType := declType
		if n.Type == nil || typeExprHasExplicitMutableRef(n.Type) {
			aliasAccessType = bindingType
		} else if _, ok := aliasAccessType.(*RefType); !ok {
			aliasAccessType = bindingType
		}
		if n.Type != nil && (typeExprHasExplicitMutableRef(n.Type) || (n.Mutable && aliasExprIsExplicitBorrow(n.Value))) {
			if ref, ok := aliasAccessType.(*RefType); ok && !ref.Mutable {
				cloned := cloneRefType(ref)
				cloned.Mutable = true
				aliasAccessType = cloned
			}
		}
		a.recordLocalRefAliasBinding(n, sym, n.Value, aliasAccessType)
		if from, fromType, ok := a.freezeMovedPackedStoreSource(n.Value); ok {
			a.remapPackedStoreDependencies(from, sym, PackedEnumStoreWithState(fromType, a.namedTypes["Frozen"]))
		}
		if n.Value != nil && AssignableTo(bindingType, valueType) {
			a.bindActivePackedStoreType(bindingType)
		}
		a.consumeAffineValueExpr(n.Value, bindingType, "move into local "+strconvQuote(n.Name))
	case *ast.LetDestructureStmt:
		a.analyzeLetDestructureStmt(n)
	case *ast.TupleBindStmt:
		var expectedTuple Type
		targetTypes := make([]Type, len(n.Names))
		if !n.Declare {
			expectedFields := make([]TupleField, 0, len(n.Names))
			for i, binding := range n.Names {
				if binding.Name == "_" {
					expectedFields = append(expectedFields, TupleField{Name: binding.Name})
					continue
				}
				target := &ast.Ident{Position: binding.Position, Name: binding.Name}
				targetTypes[i] = a.assignmentTargetType(target)
				expectedFields = append(expectedFields, TupleField{Name: binding.Name, Type: targetTypes[i]})
			}
			expectedTuple = &TupleType{Fields: expectedFields}
		}
		valueType := a.analyzeValueExpr(n.Value, expectedTuple)
		fields, ok := a.resolvedStructFields(valueType)
		if !ok {
			a.errorf(n.Pos(), "tuple destructuring requires a tuple value, got %s", valueType)
			return
		}
		if _, isTuple := StripAggregateStateType(valueType).(*TupleType); !isTuple {
			a.errorf(n.Pos(), "tuple destructuring requires a tuple value, got %s", valueType)
			return
		}
		if len(n.Names) != len(fields) {
			a.errorf(n.Pos(), "tuple destructuring expects %d bindings, got %d", len(fields), len(n.Names))
		}
		limit := len(n.Names)
		if len(fields) < limit {
			limit = len(fields)
		}
		for i := 0; i < limit; i++ {
			binding := n.Names[i]
			fieldExpr := &ast.FieldExpr{Position: binding.Position, Object: n.Value, Field: fields[i].Name}
			a.recordAnalyzedExprType(fieldExpr, fields[i].Type)
			if binding.Name == "_" {
				if n.Declare {
					a.consumeAffineValueExpr(fieldExpr, fields[i].Type, "discard tuple element")
				}
				continue
			}
			if n.Declare {
				sym := &Symbol{Name: binding.Name, Kind: SymbolLocal, Type: fields[i].Type, Node: n, Mutable: false}
				a.defineLocal(sym, binding.Position)
				a.recordValueBinding(sym, fieldExpr)
				a.recordFunctionValueBinding(sym, fieldExpr)
				a.recordImmutableSymbolOptimizationFacts(sym, fieldExpr)
				a.recordBorrowedOwnerRefBinding(sym, fieldExpr)
				a.recordRegionRefBinding(sym, fieldExpr)
				a.recordStorageViewBinding(sym, fieldExpr)
				continue
			}
			target := &ast.Ident{Position: binding.Position, Name: binding.Name}
			targetType := targetTypes[i]
			if targetType == nil {
				targetType = a.assignmentTargetType(target)
			}
			a.clearPackedVariantViewExpr(target)
			if !AssignableTo(targetType, fields[i].Type) {
				a.errorf(binding.Position, "cannot assign %s to %s", fields[i].Type, targetType)
				a.reportShapeMismatchNotes(binding.Position, targetType, fields[i].Type)
			}
			a.recordAssignmentRefinement(target, targetType, fields[i].Type)
			a.recordRegionRefAssignment(target, fieldExpr)
			a.recordStorageViewAssignment(target, fieldExpr)
			a.recordSpecializedValueTypeTarget(target, fields[i].Type)
			a.recordNamedStateAssignmentTarget(target, fieldExpr, fields[i].Type)
			a.clearAffineValueTarget(target)
			a.trackAffineValueTarget(target, targetType)
			a.markCreatedProtocolTarget(target, fieldExpr, targetType)
			a.recordBorrowedOwnerRefTarget(target, targetType, fieldExpr)
			a.recordFunctionValueTarget(target, fieldExpr)
			if AssignableTo(targetType, fields[i].Type) {
				a.bindActivePackedStoreType(targetType)
			}
			a.consumeAffineValueExpr(fieldExpr, targetType, "assignment")
		}
	case *ast.MoveBindStmt:
		a.analyzeMoveBindStmt(n)
	case *ast.DeferStmt:
		a.analyzeDeferStmt(n)
	case *ast.ScopeStmt:
		a.analyzeScopeStmt(n)
	case *ast.RegionStmt:
		if n.UserAuto {
			a.errorf(n.Pos(), "`in auto:` is no longer supported: allocations infer their region by default, so the block is not needed — remove it, or use `region NAME(size):` for an explicit scope")
		}
		if len(n.Body) != 0 || n.OwnerName != "" {
			a.analyzeScopedArenaStmt(n)
		} else {
			a.analyzeRegionDecl(n)
		}
	case *ast.MarkStmt:
		a.analyzeMarkStmt(n)
	case *ast.CheckpointStmt:
		a.analyzeCheckpointStmt(n)
	case *ast.GroupedCheckpointStmt:
		a.analyzeGroupedCheckpointStmt(n)
	case *ast.RestoreStmt:
		a.analyzeRestoreStmt(n)
	case *ast.RestoreCheckpointStmt:
		a.analyzeRestoreCheckpointStmt(n)
	case *ast.ResetStmt:
		a.analyzeResetStmt(n)
	case *ast.DestroyStmt:
		sym, state := a.lookupRegionState(n.Name)
		if sym == nil {
			a.errorf(n.Pos(), "undefined region %q", n.Name)
			return
		}
		if state.Destroyed {
			a.errorf(n.Pos(), "region %q has already been destroyed", n.Name)
			return
		}
		before := state.Generation
		state.Destroyed = true
		a.currentRegions[sym] = state
		a.invalidateRegionRefs(sym, func(regionDependencyState) bool { return true }, fmt.Sprintf("destroy of region %q", n.Name))
		a.invalidateRegionMarks(sym, func(regionMarkState) bool { return true }, fmt.Sprintf("destroy of region %q", n.Name))
		a.recordRegionInvalidateTransform(n.Pos(), n.Name, "", "destroy region", before, -1)
		// destroy discharges the region owner's must-consume obligation.
		a.recordAffineConsumption(affineValueKey{Root: sym}, "destroy")
	case *ast.LeakStmt:
		sym, state := a.lookupRegionState(n.Name)
		if sym == nil {
			a.errorf(n.Pos(), "undefined region %q", n.Name)
			return
		}
		if state.Destroyed {
			a.errorf(n.Pos(), "region %q has already been destroyed", n.Name)
			return
		}
		// leak discharges the must-consume obligation WITHOUT freeing the region
		// (its refs stay valid). It is an explicit, audited memory-safety opt-out
		// gated by Unsafe.Leak.
		a.recordFunctionPermissionRefs(unsafeLeakRefs(n.Position))
		a.recordAffineConsumption(affineValueKey{Root: sym}, "leak")
	case *ast.PromoteStmt:
		a.analyzePromoteStmt(n)
	case *ast.AdoptStmt:
		a.analyzeAdoptStmt(n)
	case *ast.AssignStmt:
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
		ambientIsSynthesized := isSynthesizedAutoRegion(a.activeContainerRegionName())
		if isGlobalTarget && (a.currentAllocExpr == nil || ambientIsSynthesized) &&
			(a.currentFuncType == nil || !a.currentFuncType.RegionPolymorphic) {
			a.currentAllocExpr = &ast.Ident{Position: n.Value.Pos(), Name: "perm"}
			restoreAllocExpr = true
		}
		valueType := a.analyzeValueExpr(n.Value, targetType)
		if restoreAllocExpr {
			a.currentAllocExpr = savedAllocExpr
		}
		if !AssignableTo(targetType, valueType) {
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType, targetType)
			a.reportShapeMismatchNotes(n.Pos(), targetType, valueType)
		}
		if !n.Optional {
			a.recordAssignmentRefinement(n.Target, targetType, valueType)
			a.recordRegionRefAssignment(n.Target, n.Value)
			a.recordStorageViewAssignment(n.Target, n.Value)
		}
		if a.lvalueStorageOutlivesFunction(n.Target) {
			a.checkLocalArenaEscape(n.Value, valueType, "store")
			a.checkStoredRegionContainerEscape(n.Value, valueType)
		}
		// Nested-region escape: storing an inner-@r value into an outer-region
		// slot dangles once the inner region is freed. Decided by the region
		// outlives-lattice, independent of whether the target outlives the
		// function (the function-outliving case is handled just above).
		a.checkNestedRegionStoreEscape(n.Target, targetType, valueType)
		// A list literal `[v]` stored into a longer-lived container copies an inner-region element's
		// header; the store-site check above only sees the literal's own (target) region, so the
		// element regions are checked separately.
		a.checkFreshContainerStoreEscape(n.Target, targetType, n.Value)
		// Struct copy-by-value can launder a dangling interior region-container
		// field (whose region is not on the struct type) past the region's death;
		// the interior-taint side-table makes that region visible to the check.
		a.checkStructCopyInteriorRegionEscape(n.Target, targetType, n.Value, valueType)
		a.recordStructInteriorRegionTaint(n.Target, n.Value, valueType)
		a.checkStoredBorrowEscapesLocal(n.Target, targetType, n.Value, valueType)
		if ident, ok := n.Target.(*ast.Ident); ok && a.currentScope != nil {
			if targetSym, ok := a.currentScope.Lookup(ident.Name); ok {
				if from, fromType, ok := a.freezeMovedPackedStoreSource(n.Value); ok {
					a.remapPackedStoreDependencies(from, targetSym, PackedEnumStoreWithState(fromType, a.namedTypes["Frozen"]))
				}
			}
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
			a.clearZeroedUninitializedForExpr(n.Target)
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
		valueType := a.analyzeExpr(n.Value)
		if !IsNumericType(targetType) || !IsNumericType(valueType) {
			a.errorf(n.Pos(), "augmented assignment requires numeric operands")
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
		a.clearZeroedUninitializedForExpr(n.Target)
		a.clearAffineValueTarget(n.Target)
		a.trackAffineValueTarget(n.Target, targetType)
		a.markCreatedProtocolTarget(n.Target, n.Value, targetType)
		a.recordBorrowedOwnerRefTarget(n.Target, targetType, n.Value)
		a.recordFunctionValueTarget(n.Target, n.Value)
		if AssignableTo(targetType, valueType) {
			a.bindActivePackedStoreType(targetType)
		}
		a.consumeAffineValueExpr(n.Value, targetType, "assignment")
		a.invalidateSMTAssertFactsForTarget(n.Target)
		a.recordSMTAssignmentFact(n.Target, n.Value)
	case *ast.ReturnStmt:
		// docs/85 brick 2 (B): check `ensures <param> is Law` postconditions on every return path,
		// including value-less returns in a void function, before the value-specific handling below.
		a.dischargeEnsuresRefinements(n)
		if n.Value == nil {
			if currentUnion, ok := a.currentReturn.(*ErrorUnionType); ok {
				if !SameType(currentUnion.Value, a.namedTypes["void"]) {
					a.errorf(n.Pos(), "return value required for %s", a.currentReturn)
				}
				a.validateCurrentFuncPoststates()
				return
			}
			if a.currentReturn != nil && !SameType(a.currentReturn, a.namedTypes["void"]) {
				a.errorf(n.Pos(), "return value required for %s", a.currentReturn)
			}
			a.validateCurrentFuncPoststates()
			return
		}
		valueType := a.analyzeValueExpr(n.Value, a.currentReturn)
		if a.currentReturn == nil {
			a.errorf(n.Pos(), "unexpected return value")
			return
		}
		a.dischargeReturnRefinements(n)
		a.dischargeReturnWhereRefinement(n)
		a.dischargeEnsureBooleans(n)
		a.checkLocalArenaEscape(n.Value, valueType, "return")
		a.checkReturnBorrowEscapesLocal(n.Value, valueType)
		a.checkReturnRegionContainerEscape(n.Value, valueType)
		a.checkRegionAggregateReturnEscape(n.Value, valueType)
		a.checkRegionParamReturnEscape(n.Value, valueType)
		// Approach A: `return move <region>` transfers an owned region to the
		// caller. Consume it locally (discharging the must-consume obligation)
		// and mark the function as returning an owned region. All value-returns
		// must agree, or the caller's inherited obligation would be unsound.
		if ownerSym, isOwned := a.returnedRegionOwner(n.Value); isOwned {
			if a.currentFuncSawPlainValueReturn {
				a.errorf(n.Pos(), "this path returns an owned region but another returns a plain value; every value-returning path must transfer the region with `return move <region>` or none may")
			}
			a.recordAffineConsumption(affineValueKey{Root: ownerSym}, "return")
			if a.currentFuncType != nil {
				a.currentFuncType.ReturnsOwnedRegion = true
			}
		} else {
			if a.currentFuncType != nil && a.currentFuncType.ReturnsOwnedRegion {
				a.errorf(n.Pos(), "this path returns a plain value but another returns an owned region; every value-returning path must transfer the region with `return move <region>` or none may")
			}
			a.currentFuncSawPlainValueReturn = true
		}
		if refState, ok := a.regionRefStateForExpr(n.Value); ok {
			if region, _, ok := firstLiveRegionDependency(refState); ok && region != nil {
				// docs/75 step 1 (detection): a value built with `new[auto]` carries a
				// synthesized inferred region (`__auto_*`) local to this function. Returning
				// it is the signature of a *region-polymorphic* function — the region will be
				// threaded from the caller so the result outlives the call. Record that
				// classification now. The escape error still fires until threading (steps
				// 2-3) makes the return sound; an explicitly-named local region (`in
				// scratch:`) is never region-polymorphic and always errors.
				regionPoly := a.currentFuncType != nil && a.currentFuncType.RegionPolymorphic
				if isSynthesizedAutoRegion(region.Name) && a.currentFuncType != nil {
					// Belt-and-suspenders: the pre-pass already set this from a syntactic scan, but
					// keep the flow-based confirmation so the classification never under-reports.
					a.currentFuncType.RegionPolymorphic = true
					regionPoly = true
				}
				// docs/75: in a region-polymorphic function the synthesized `__auto_*` region is
				// threaded from the caller (the hidden `__region_auto` Arena& param), so the result
				// outlives the call — no escape. Suppress the error. Explicitly-named local regions
				// are never region-polymorphic and still error.
				if !regionPoly {
					if _, isRef := valueType.(*RefType); isRef {
						a.errorf(n.Pos(), localRegionEscapeMessage("reference", region.Name))
					} else {
						a.errorf(n.Pos(), localRegionEscapeMessage("value", region.Name))
					}
				}
			}
			if summary, ok := abstractParamOnlyRegionRefState(refState); ok {
				if merged, ok := mergeRegionRefStates(a.currentReturnProvenance, summary); ok {
					a.currentReturnProvenance = merged
				} else if !hasRegionProvenance(a.currentReturnProvenance) {
					a.currentReturnProvenance = cloneRegionRefState(summary)
				}
			}
		}
		a.inReturnBorrowCapture = true
		ownerState, ownerStateOK := a.borrowedOwnerRefStateForExpr(n.Value)
		a.inReturnBorrowCapture = false
		if ownerStateOK {
			if summary, ok := abstractParamOnlyBorrowedOwnerRefSummary(ownerState); ok {
				if merged, ok := mergeBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs, summary); ok {
					a.currentReturnBorrowedOwnerRefs = merged
				} else if !hasBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs) {
					a.currentReturnBorrowedOwnerRefs = summary
				}
			}
		}
		a.recordFreshReturnBindings(valueType)
		expectedReturn := a.matchReturnType(valueType)
		if !AssignableTo(expectedReturn, valueType) {
			a.errorf(n.Pos(), "return type expects %s, got %s", expectedReturn, valueType)
			a.reportShapeMismatchNotes(n.Pos(), expectedReturn, valueType)
		}
		a.validateCurrentFuncPoststatesForReturnValue(n.Value)
		a.consumeAffineValueExpr(n.Value, expectedReturn, "return")
	case *ast.BreakStmt:
		if a.loopDepth == 0 {
			a.errorf(n.Pos(), "break is only valid inside a loop")
		}
	case *ast.ContinueStmt:
		if a.loopDepth == 0 {
			a.errorf(n.Pos(), "continue is only valid inside a loop")
		}
	case *ast.IfStmt:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "if condition must be bool, got %s", condType)
		}
		// The post-if affine state is the meet of the branch outcomes, NOT the
		// entry state unioned with them. Seeding with entry would keep a value
		// "live" even when every arm consumes it (a false "must be consumed"),
		// because mergeAffineValueStates only ever adds liveness. The skip path
		// (condition false with no else) is already supplied by the empty-else
		// snapshot that is analyzed and merged below, so entry survives the join
		// only when it actually should.
		entryAffine := a.cloneAffineValueStates()
		var mergedAffine map[affineValueKey]affineValueState
		mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
		mergedStorageViewDeps := a.cloneStorageViewDeps()
		var mergedAliasCarriers map[string][]string
		var mergedAliasCarrierFieldOverrides map[string]map[string][]string
		functionValueBranches := make([]map[*Symbol]*FuncType, 0, len(n.Elifs)+2)
		specializedValueTypeBranches := make([]map[*Symbol]Type, 0, len(n.Elifs)+2)
		entryRangeFacts := a.visibleRangeFacts()
		rangeBranches := make([]map[string]numRange, 0, len(n.Elifs)+2)
		thenSnapshot := a.analyzeBlockWithConditionAffineClone(n.Then, a.currentScope, n.Cond, true)
		if !blockDefinitelyExits(n.Then) {
			mergedAffine = mergeAffineValueStates(mergedAffine, thenSnapshot.Affine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, thenSnapshot.BorrowedOwnerRefs)
			mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, thenSnapshot.StorageViewDeps)
			mergedAliasCarrierFieldOverrides = mergeAliasCarrierFieldOverrides(mergedAliasCarriers, mergedAliasCarrierFieldOverrides, thenSnapshot.AliasCarriers, thenSnapshot.AliasCarrierFieldOverrides)
			mergedAliasCarriers = mergeAliasCarriers(mergedAliasCarriers, thenSnapshot.AliasCarriers)
			functionValueBranches = append(functionValueBranches, thenSnapshot.FunctionValues)
			specializedValueTypeBranches = append(specializedValueTypeBranches, thenSnapshot.SpecializedValueTypes)
			rangeBranches = append(rangeBranches, thenSnapshot.RangeFacts)
		}
		for _, elif := range n.Elifs {
			elifType := a.analyzeExpr(elif.Cond)
			if !IsBoolType(elifType) {
				a.errorf(elif.Position, "elif condition must be bool, got %s", elifType)
			}
			elifSnapshot := a.analyzeBlockWithConditionAffineClone(elif.Body, a.currentScope, elif.Cond, true)
			if !blockDefinitelyExits(elif.Body) {
				mergedAffine = mergeAffineValueStates(mergedAffine, elifSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, elifSnapshot.BorrowedOwnerRefs)
				mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, elifSnapshot.StorageViewDeps)
				mergedAliasCarrierFieldOverrides = mergeAliasCarrierFieldOverrides(mergedAliasCarriers, mergedAliasCarrierFieldOverrides, elifSnapshot.AliasCarriers, elifSnapshot.AliasCarrierFieldOverrides)
				mergedAliasCarriers = mergeAliasCarriers(mergedAliasCarriers, elifSnapshot.AliasCarriers)
				functionValueBranches = append(functionValueBranches, elifSnapshot.FunctionValues)
				specializedValueTypeBranches = append(specializedValueTypeBranches, elifSnapshot.SpecializedValueTypes)
				rangeBranches = append(rangeBranches, elifSnapshot.RangeFacts)
			}
		}
		if len(n.Elifs) == 0 {
			elseSnapshot := a.analyzeBlockWithConditionAffineClone(n.Else, a.currentScope, n.Cond, false)
			if !blockDefinitelyExits(n.Else) {
				mergedAffine = mergeAffineValueStates(mergedAffine, elseSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, elseSnapshot.BorrowedOwnerRefs)
				mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, elseSnapshot.StorageViewDeps)
				mergedAliasCarrierFieldOverrides = mergeAliasCarrierFieldOverrides(mergedAliasCarriers, mergedAliasCarrierFieldOverrides, elseSnapshot.AliasCarriers, elseSnapshot.AliasCarrierFieldOverrides)
				mergedAliasCarriers = mergeAliasCarriers(mergedAliasCarriers, elseSnapshot.AliasCarriers)
				functionValueBranches = append(functionValueBranches, elseSnapshot.FunctionValues)
				specializedValueTypeBranches = append(specializedValueTypeBranches, elseSnapshot.SpecializedValueTypes)
				rangeBranches = append(rangeBranches, elseSnapshot.RangeFacts)
			}
		} else {
			elseSnapshot := a.analyzeBranchBlockWithAffineClone(n.Else, NewScope(a.currentScope))
			if !blockDefinitelyExits(n.Else) {
				mergedAffine = mergeAffineValueStates(mergedAffine, elseSnapshot.Affine)
				mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, elseSnapshot.BorrowedOwnerRefs)
				mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, elseSnapshot.StorageViewDeps)
				mergedAliasCarrierFieldOverrides = mergeAliasCarrierFieldOverrides(mergedAliasCarriers, mergedAliasCarrierFieldOverrides, elseSnapshot.AliasCarriers, elseSnapshot.AliasCarrierFieldOverrides)
				mergedAliasCarriers = mergeAliasCarriers(mergedAliasCarriers, elseSnapshot.AliasCarriers)
				functionValueBranches = append(functionValueBranches, elseSnapshot.FunctionValues)
				specializedValueTypeBranches = append(specializedValueTypeBranches, elseSnapshot.SpecializedValueTypes)
				rangeBranches = append(rangeBranches, elseSnapshot.RangeFacts)
			}
		}
		if len(n.Else) == 0 {
			functionValueBranches = append(functionValueBranches, a.currentFunctionValues)
			specializedValueTypeBranches = append(specializedValueTypeBranches, a.currentSpecializedValueTypes)
			rangeBranches = append(rangeBranches, entryRangeFacts)
		}
		if mergedAffine == nil {
			// Every taken branch diverged and an else covered the remaining
			// case: code after the if is unreachable. Preserve entry so the
			// flow state stays non-nil for downstream analysis.
			mergedAffine = entryAffine
		}
		a.currentAffineValues = mergedAffine
		a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
		a.currentStorageViewDeps = mergedStorageViewDeps
		a.currentAliasCarriers = mergedAliasCarriers
		a.currentAliasCarrierFieldOverrides = mergedAliasCarrierFieldOverrides
		if mergedFunctionValues, ok := a.intersectFunctionValueFlows(functionValueBranches...); ok {
			a.currentFunctionValues = mergedFunctionValues
		}
		if mergedSpecializedValueTypes, ok := a.intersectSpecializedValueTypeFlows(specializedValueTypeBranches...); ok {
			a.currentSpecializedValueTypes = mergedSpecializedValueTypes
		}
		a.mergePostIfRangeFacts(entryRangeFacts, rangeBranches)
		a.applyPostIfFallthroughRefinement(n)
		if len(n.Elifs) == 0 && len(n.Else) == 0 && blockDefinitelyExits(n.Then) {
			a.applyIndexBoundsFactsForCondition(n.Cond, false)
		}
	case *ast.MatchStmt:
		a.analyzeMatchStmt(n)
	case *ast.ExpectPatternStmt:
		a.analyzeExpectPatternStmt(n)
	case *ast.InStoreStmt:
		a.analyzeInStoreStmt(n)
	case *ast.CanStmt:
		a.analyzeCanStmt(n)
	case *ast.PoolStmt:
		a.analyzePoolStmt(n)
	case *ast.LockStmt:
		a.analyzeLockStmt(n)
	case *ast.WhileStmt:
		bodyPermissionRefStart := len(a.currentFunctionUsedPermissionRefs)
		progressObligationIndex := a.recordProgressLoopObligation(n)
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "while condition must be bool, got %s", condType)
		}
		mergedAffine := a.cloneAffineValueStates()
		mergedBorrowedOwnerRefs := a.cloneBorrowedOwnerRefBindings()
		mergedFunctionValues := a.cloneFunctionValueBindings()
		mergedSpecializedValueTypes := a.cloneSpecializedValueTypeBindings()
		mergedStorageViewDeps := a.cloneStorageViewDeps()
		// Verify leading `invariant` clauses inductively (establishment + preservation) BEFORE the body
		// is analyzed — the body's own mutations (`i <- i + 1`) invalidate the entry facts upward, so
		// establishment must read the pristine pre-loop scope. Exit facts are seeded after the body.
		provenInvariants, loopExitFactsSound := a.proveLoopInvariants(n)
		// Verify a leading `decreases` measure proves the loop terminates (strict decrease + bounded
		// below on every iteration), on the same pristine pre-loop scope.
		a.checkLoopTermination(n)
		// The count-up exit fact `i <= bound` is sound only if `i <= bound` at ENTRY; evaluate on the
		// pristine pre-loop scope, since the body's `i <- i + 1` mutates i's tracked value below.
		countUpExitSound := a.countUpExitFactSound(n)
		a.loopDepth++
		bodySnapshot := a.analyzeBlockWithConditionAffineClone(n.Body, a.currentScope, n.Cond, true)
		a.loopDepth--
		a.finishProgressLoopObligation(progressObligationIndex, a.currentFunctionUsedPermissionRefs[bodyPermissionRefStart:])
		if !blockDefinitelyExits(n.Body) {
			mergedAffine = mergeAffineValueStates(mergedAffine, bodySnapshot.Affine)
			mergedBorrowedOwnerRefs = mergeBorrowedOwnerRefBindings(mergedBorrowedOwnerRefs, bodySnapshot.BorrowedOwnerRefs)
			mergedFunctionValues = a.mergeFunctionValueBindings(mergedFunctionValues, bodySnapshot.FunctionValues)
			mergedSpecializedValueTypes = a.mergeSpecializedValueTypeBindings(mergedSpecializedValueTypes, bodySnapshot.SpecializedValueTypes)
			mergedStorageViewDeps = mergeStorageViewDependencyStates(mergedStorageViewDeps, bodySnapshot.StorageViewDeps)
		}
		a.currentAffineValues = mergedAffine
		a.currentBorrowedOwnerRefs = mergedBorrowedOwnerRefs
		a.currentFunctionValues = mergedFunctionValues
		a.currentSpecializedValueTypes = mergedSpecializedValueTypes
		a.currentStorageViewDeps = mergedStorageViewDeps
		// Export `inv ∧ ¬cond` as after-loop facts when every invariant was proven inductive above.
		if loopExitFactsSound {
			a.seedLoopExitFacts(n, provenInvariants)
		}
		if countUpExitSound {
			a.applyCountUpWhileExitFacts(n)
		}
	case *ast.ForStmt:
		a.analyzeForStmt(n)
	case *ast.IterForStmt:
		a.analyzeIterForStmt(n)
	case *ast.ParallelForStmt:
		a.analyzeParallelForStmt(n)
	case *ast.PassStmt:
		return
	case *ast.SignalStmt:
		refs := a.resolvePermissionRefs(n.Permissions, true)
		a.recordFunctionPermissionRefs(refs)
		return
	case *ast.PanicStmt:
		a.analyzeExpr(n.Message)
	case *ast.ExprStmt:
		if cond, ok := assertedCondition(n.Expr); ok {
			// An `assert` is a contract: ghost vars are readable here (it is debug-checked / erased
			// in release, never observable by real values).
			a.ghostReadAllowed++
			a.ghostReadSeen = false
			defer func() { a.ghostReadAllowed-- }()
			condType := a.analyzeCondExpr(cond)
			if a.ghostReadSeen {
				a.markGhostContract(n.Expr)
			}
			if !IsBoolType(condType) {
				a.errorf(n.Pos(), "assert condition must be bool, got %s", condType)
			}
			// docs/98 — proof holes: under strict proofs a plain `assert` is held to the same
			// prove-it-or-fail bar as `assert … by:`. Discharge BEFORE the cond is recorded as a
			// downstream fact (else it trivially entails itself); on failure emit the constructive
			// goal / known-facts / suggested-missing-fact report.
			// docs/98/101 — route a plain `assert COND` through the SAME static discharge ladder
			// `assert … by:` uses (facts/intervals → linear → SMT). On success a proof is recorded; on
			// decline the assert REMAINS a runtime check (no hard error). The constructive proof-hole hint
			// (opt-in) is then offered only when the goal was NOT discharged here.
			proven := a.dischargePlainAssert(n.Pos(), cond)
			a.checkStrictAssertProofHole(n.Pos(), cond, proven)
			a.applyConditionRefinements(a.currentScope, cond, true)
			a.applyIndexBoundsFactsForCondition(cond, true)
			a.recordSMTAssertFact(cond)
			return
		}
		exprType := a.analyzeExpr(n.Expr)
		// An error union must be handled (`try`/`match`/`catch`) or propagated
		// (`return`/`raise`); it cannot be silently dropped as a bare statement, or its
		// error — and any owned payload it carries — is swallowed. `try`/`raise` already
		// reduce to the ok type, so only a raw dropped call surfaces here.
		if isUnhandledErrorUnionType(exprType) {
			a.errorf(n.Pos(), "error union result must be handled with `try`, `match`, or `catch`, or propagated with `return`; it cannot be silently dropped")
		}
	case *ast.StaticIfStmt:
		for _, stmt := range a.activeStmtBranch(n) {
			a.analyzeStmt(stmt)
		}
	case *ast.StaticErrorStmt:
		if a.currentFuncDecl != nil && a.currentFuncDecl.Static {
			a.analyzeExpr(n.Message)
			return
		}
		if msg, ok := a.evalConstStringExpr(n.Message); ok {
			a.errorf(n.Pos(), "static error: %s", msg)
		} else {
			a.errorf(n.Pos(), "static error triggered")
		}
	case *ast.ContractStmt:
		// In-body `invariant` is analyzed + checked in place; a `requires`/`ensure` reaching here
		// (not lifted) was not at the function start, which is the only place they're honoured.
		if n.Kind == ast.ContractDecreases {
			return
		}
		if n.Kind != ast.ContractInvariant {
			a.errorf(n.Pos(), "`requires`/`ensure` contracts must be the first statements of the function body")
		} else if n.Cond != nil {
			a.ghostReadAllowed++
			a.ghostReadSeen = false
			defer func() { a.ghostReadAllowed-- }()
			condType := a.analyzeCondExpr(n.Cond)
			if a.ghostReadSeen {
				a.markGhostContract(n.Cond)
			}
			if condType != nil && !IsBoolType(condType) {
				a.errorf(n.Cond.Pos(), "invariant must be bool, got %s", condType)
			}
			a.applyConditionRefinements(a.currentScope, n.Cond, true)
			a.applyIndexBoundsFactsForCondition(n.Cond, true)
			if a.canAssumeContractFact(n.Cond) {
				a.recordSMTAssertFact(n.Cond)
			}
		}
	case *ast.AssertByStmt:
		a.analyzeAssertBy(n)
	case *ast.ProofBlockStmt:
		a.analyzeProofBlock(n)
	case *ast.AssertHoleStmt:
		a.emitAssertHole(n.Pos())
	case *ast.ProofUseStmt:
		a.errorf(n.Pos(), "`use` citations are only allowed inside an `assert … by:` proof block")
	case *ast.StaticAssertStmt:
		a.analyzeStaticAssert(n.Pos(), n.Cond, n.Message)
	case *ast.StaticAssertBlockStmt:
		for _, item := range n.Assertions {
			a.analyzeStaticAssert(item.Position, item.Cond, item.Message)
		}
	case *ast.StaticBlockStmt:
		a.analyzeStaticOnlyStmts(n.Body)
	case *ast.DiscardStmt:
		valueType := a.analyzeExpr(n.Value)
		// `_ = f()` discards the value; for an error union that swallows the error (and
		// any owned payload). The correct discard is `_ = try f() else …` (handle first,
		// then drop the ok value).
		if isUnhandledErrorUnionType(valueType) {
			a.errorf(n.Pos(), "error union result must be handled with `try`, `match`, or `catch`; discarding it with `_ =` swallows the error")
		}
		a.consumeAffineValueExpr(n.Value, valueType, "discard")
	}
}

// ghostInitIsErasureSafe reports whether a ghost variable's initializer is a side-effect-free
// expression that is safe to erase from codegen. It permits reads (idents, literals), arithmetic and
// comparisons, parenthesization, field/index access, and the contract pseudo-call `old(...)`. A
// general call is rejected, since it might mutate real state whose effect erasure would silently
// drop. This mirrors the "verification code must be pure" property the lemma machinery relies on.
// markGhostContract flags a contract condition that reads a ghost variable, so codegen erases its
// runtime check (a ghost has no runtime storage; the obligation is already discharged statically).
func (a *Analyzer) markGhostContract(cond ast.Expr) {
	if cond == nil {
		return
	}
	if a.ghostContracts == nil {
		a.ghostContracts = map[ast.Expr]bool{}
	}
	a.ghostContracts[cond] = true
}

func ghostInitIsErasureSafe(expr ast.Expr) bool {
	switch n := expr.(type) {
	case nil:
		return true
	case *ast.Ident, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.CharLit, *ast.StringLit, *ast.NullLit:
		return true
	case *ast.ParenExpr:
		return ghostInitIsErasureSafe(n.Inner)
	case *ast.UnaryExpr:
		return ghostInitIsErasureSafe(n.Operand)
	case *ast.BinaryExpr:
		return ghostInitIsErasureSafe(n.Left) && ghostInitIsErasureSafe(n.Right)
	case *ast.FieldExpr:
		return ghostInitIsErasureSafe(n.Object)
	case *ast.IndexExpr:
		return ghostInitIsErasureSafe(n.Object) && ghostInitIsErasureSafe(n.Index)
	case *ast.QuantifierExpr:
		return ghostInitIsErasureSafe(n.Body)
	case *ast.CallExpr:
		// Only the contract pseudo-call `old(expr)` is allowed; it has no runtime effect (the backend
		// snapshots its argument at entry for `ensure`, and it is itself ghost here).
		if id, ok := n.Func.(*ast.Ident); ok && id.Name == "old" {
			for _, arg := range n.Args {
				if !ghostInitIsErasureSafe(arg) {
					return false
				}
			}
			return true
		}
		return false
	default:
		return false
	}
}

func concreteDArrayViewBindingType(declared Type, actual Type) (Type, bool) {
	declaredView, ok := declared.(*ViewType)
	if !ok {
		return nil, false
	}
	actualView, ok := actual.(*ViewType)
	if !ok {
		return nil, false
	}
	if !SameType(declaredView.Elem, actualView.Elem) {
		return nil, false
	}
	return actualView, true
}

func (a *Analyzer) analyzeMoveBindStmt(stmt *ast.MoveBindStmt) {
	if stmt == nil {
		return
	}
	valueType := a.analyzeExpr(stmt.Value)
	valueState, hasValueState := a.regionRefStateForExpr(stmt.Value)
	borrowedOwnerState, hasBorrowedOwnerState := a.borrowedOwnerRefStateForExpr(stmt.Value)
	switch p := stmt.Pattern.(type) {
	case *ast.MoveBindNamePattern:
		if p.Name != "_" {
			sym := &Symbol{Name: p.Name, Kind: SymbolLocal, Type: valueType, Node: p, Mutable: false}
			a.defineLocal(sym, p.Pos())
			a.recordValueBinding(sym, stmt.Value)
			a.recordFunctionValueBinding(sym, stmt.Value)
			a.recordImmutableSymbolOptimizationFacts(sym, stmt.Value)
			if hasBorrowedOwnerState {
				a.currentBorrowedOwnerRefs[sym] = borrowedOwnerState
			}
			if hasValueState {
				a.recordResolvedRegionRefBinding(sym, valueState)
			} else {
				a.recordRegionRefBinding(sym, stmt.Value)
			}
		}
	case *ast.MoveBindStructPattern:
		fields, ok := a.resolveMoveBindStructPattern(p, valueType)
		if !ok {
			return
		}
		for i, arg := range p.Args {
			if i >= len(fields) || arg.Name == "_" {
				continue
			}
			sym := &Symbol{Name: arg.Name, Kind: SymbolLocal, Type: fields[i].Type, Node: p, Mutable: false}
			a.defineLocal(sym, arg.Position)
			fieldExpr := &ast.FieldExpr{Position: arg.Position, Object: stmt.Value, Field: fields[i].Name}
			a.recordValueBinding(sym, fieldExpr)
			a.recordFunctionValueBinding(sym, fieldExpr)
			a.recordImmutableSymbolOptimizationFacts(sym, fieldExpr)
			if hasBorrowedOwnerState {
				if fieldState, ok := projectBorrowedOwnerRefFieldState(borrowedOwnerState, fields[i].Name); ok {
					a.currentBorrowedOwnerRefs[sym] = fieldState
				}
			}
			if !hasValueState {
				continue
			}
			if fieldState, ok := projectRegionFieldState(valueState, fields[i].Name); ok {
				a.recordResolvedRegionRefBinding(sym, fieldState)
			}
		}
	case *ast.MoveBindVariantPattern:
		payloads, enumType, packedStoreState, ok := a.resolveMoveBindVariantPattern(stmt, p, valueType)
		if !ok {
			return
		}
		_ = enumType
		a.bindResolvedMoveBindVariantFields(payloads, stmt.Value, p, p, valueState, hasValueState, borrowedOwnerState, hasBorrowedOwnerState, packedStoreState)
	default:
		a.errorf(stmt.Pos(), "unsupported move-as pattern %T", stmt.Pattern)
		return
	}
	a.consumeAffineValueExpr(stmt.Value, valueType, "move-as destructure")
}
